package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/i18n"
	internalIO "github.com/lxc/incus/v7/internal/io"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/ioprogress"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

// sftpSetOwnerMode applies the ownership and mode to a remote path on a best effort basis.
func sftpSetOwnerMode(sftpConn *sftp.Client, targetPath string, args incus.InstanceFileArgs) error {
	// Skip if not on UNIX.
	_, err := sftpConn.StatVFS("/")
	if err != nil {
		return nil
	}

	// Get the current stat information.
	st, err := sftpConn.Stat(targetPath)
	if err != nil {
		return err
	}

	fileStat, ok := st.Sys().(*sftp.FileStat)
	if !ok {
		return fmt.Errorf("Invalid filestat data for %q", targetPath)
	}

	// Set owner.
	if args.UID >= 0 || args.GID >= 0 {
		if args.UID == -1 {
			args.UID = int64(fileStat.UID)
		}

		if args.GID == -1 {
			args.GID = int64(fileStat.GID)
		}

		err = sftpConn.Chown(targetPath, int(args.UID), int(args.GID))
		if err != nil {
			logger.Infof("Failed to set owner on %s: %v", targetPath, err)
		}
	}

	// Set mode.
	if args.Mode >= 0 {
		err = sftpConn.Chmod(targetPath, fs.FileMode(args.Mode))
		if err != nil {
			logger.Infof("Failed to set mode on %s: %v", targetPath, err)
		}
	}

	return nil
}

// sftpSetLocalAttrs applies the remote ownership, mode and timestamps to a local path on a best effort basis.
func sftpSetLocalAttrs(target string, fInfo os.FileInfo) {
	fileStat, ok := fInfo.Sys().(*sftp.FileStat)

	// Set owner.
	if ok {
		err := os.Lchown(target, int(fileStat.UID), int(fileStat.GID))
		if err != nil {
			logger.Infof("Failed to set owner on %s: %v", target, err)
		}
	}

	// Set mode (symlinks don't have a mode of their own).
	if fInfo.Mode()&os.ModeSymlink == 0 {
		err := os.Chmod(target, fInfo.Mode())
		if err != nil {
			logger.Infof("Failed to set mode on %s: %v", target, err)
		}
	}

	// Set timestamps.
	if ok {
		err := internalIO.Lchtimes(target, fileStat.AccessTime(), fileStat.ModTime())
		if err != nil {
			logger.Infof("Failed to set timestamps on %s: %v", target, err)
		}
	}
}

// sftpSetRemoteTimes applies the local modification time to a remote path on a best effort basis.
func sftpSetRemoteTimes(sftpConn *sftp.Client, targetPath string, fInfo os.FileInfo) {
	err := sftpConn.Chtimes(targetPath, fInfo.ModTime(), fInfo.ModTime())
	if err != nil {
		logger.Infof("Failed to set timestamps on %s: %v", targetPath, err)
	}
}

// sftpPushArgs fills in the unset ownership and mode of args from the source, similar to cp.
// Without archive, only newly created files get the source permission bits, masked by the umask.
func sftpPushArgs(args *incus.InstanceFileArgs, fInfo os.FileInfo, archive bool, exists bool) {
	mode, uid, gid := internalIO.GetOwnerMode(fInfo)

	if !archive {
		if !exists && args.Mode == -1 {
			args.Mode = int(mode.Perm() &^ internalIO.GetUmask())
		}

		return
	}

	if args.UID == -1 {
		args.UID = int64(uid)
	}

	if args.GID == -1 {
		args.GID = int64(gid)
	}

	if args.Mode == -1 {
		args.Mode = int(mode)
	}
}

func sftpCreateFile(sftpConn *sftp.Client, targetPath string, args incus.InstanceFileArgs, push bool) error {
	switch args.Type {
	case "file":
		file, err := sftpConn.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to open target file %q: %w"), targetPath, err)
		}

		defer logger.WarnOnError(file.Close, "Failed to close file")

		if push {
			_, err = util.SafeCopy(file, args.Content)
			if err != nil {
				return err
			}
		}

		err = sftpSetOwnerMode(sftpConn, targetPath, args)
		if err != nil {
			return err
		}

	case "directory":
		err := sftpConn.MkdirAll(targetPath)
		if err != nil {
			return err
		}

		err = sftpSetOwnerMode(sftpConn, targetPath, args)
		if err != nil {
			return err
		}

	case "symlink":
		// If already a symlink, re-create it.
		fInfo, err := sftpConn.Lstat(targetPath)
		if err == nil && fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			err = sftpConn.Remove(targetPath)
			if err != nil {
				return err
			}
		}

		dest, err := io.ReadAll(args.Content)
		if err != nil {
			return err
		}

		err = sftpConn.Symlink(string(dest), targetPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// sftpRecursivePullFile pulls a remote path into targetDir and, for directories, its content.
func sftpRecursivePullFile(sftpConn *sftp.Client, fInfo os.FileInfo, source string, normalizedSource string, targetDir string, quiet bool, archive bool, dereference bool, createRoot bool) error {
	var fileType string
	if fInfo.IsDir() {
		fileType = "directory"
	} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
		fileType = "symlink"
	} else {
		fileType = "file"
	}

	target := targetDir
	if createRoot {
		root := path.Base(source)
		// `cp` has a special behavior with the following paths.
		if root == "." || root == ".." {
			root = ""
		}

		target = filepath.Join(targetDir, root)
	}

	logger.Infof("Pulling %s from %s (%s)", target, normalizedSource, fileType)

	switch fileType {
	case "directory":
		// Keep the directory writable until its content has been pulled.
		created := true
		err := os.Mkdir(target, fInfo.Mode().Perm()|0o700)
		if err != nil {
			// If the error isn’t that the path already exists, there’s nothing we can do about it.
			if !errors.Is(err, os.ErrExist) {
				return err
			}

			created = false

			// The error is pretty wide, so we must check whether the existing path it a directory (in
			// which case we can continue) or not (in which case we must fail).
			stat, statErr := os.Stat(target)
			if statErr != nil || !stat.IsDir() {
				// Even if the stat error can contain interesting data, the actual error that led us here in
				// the first place is `err`.
				return err
			}
		}

		entries, err := sftpConn.ReadDir(normalizedSource)
		if err != nil {
			return err
		}

		for _, ent := range entries {
			nextP := path.Join(normalizedSource, ent.Name())
			stat := sftpConn.Lstat
			if dereference {
				stat = sftpConn.Stat
			}

			nextInfo, err := stat(nextP)
			if err != nil {
				return err
			}

			err = sftpRecursivePullFile(sftpConn, nextInfo, nextP, nextP, target, quiet, archive, dereference, true)
			if err != nil {
				return err
			}
		}

		if archive {
			sftpSetLocalAttrs(target, fInfo)
		} else if created {
			err = os.Chmod(target, fInfo.Mode().Perm()&^internalIO.GetUmask())
			if err != nil {
				return err
			}
		}

	case "file":
		src, err := sftpConn.Open(normalizedSource)
		if err != nil {
			return err
		}

		defer logger.WarnOnErrorExcept(src.Close, []error{os.ErrClosed}, "Failed to close source file")

		// New files get the source permissions (masked by the umask), existing ones keep theirs.
		dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fInfo.Mode().Perm())
		if err != nil {
			return err
		}

		defer logger.WarnOnErrorExcept(dst.Close, []error{os.ErrClosed}, "Failed to close target file")

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), normalizedSource, target),
			Quiet:  quiet,
		}

		writer := &ioprogress.ProgressWriter{
			WriteCloser: dst,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(bytesReceived int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%s (%s/s)",
							units.GetByteSizeString(bytesReceived, 2),
							units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}

		_, err = util.SafeCopy(writer, src)
		if err != nil {
			progress.Done("")
			return err
		}

		err = src.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		err = dst.Close()
		if err != nil {
			progress.Done("")
			return err
		}

		if archive {
			sftpSetLocalAttrs(target, fInfo)
		}

		progress.Done("")
	case "symlink":
		linkTarget, err := sftpConn.ReadLink(normalizedSource)
		if err != nil {
			return err
		}

		err = os.Symlink(linkTarget, target)
		if err != nil {
			return err
		}

		if archive {
			sftpSetLocalAttrs(target, fInfo)
		}

	default:
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), fileType)
	}

	return nil
}

func sftpRecursivePushFile(sftpConn *sftp.Client, walkableSource string, source string, target string, args incus.InstanceFileArgs, quiet bool, archive bool, dereference bool, createRoot bool) error {
	root := ""
	if createRoot {
		root = filepath.Base(source)
		// `cp` has a special behavior with the following paths.
		if root == "." || root == ".." {
			root = ""
		}
	}

	return sftpRecursivePushEntry(sftpConn, walkableSource, path.Join(target, root), args, quiet, archive, dereference, true, map[string]struct{}{})
}

// sftpRecursivePushEntry pushes a local path and, for directories, its content.
func sftpRecursivePushEntry(sftpConn *sftp.Client, p string, targetPath string, args incus.InstanceFileArgs, quiet bool, archive bool, dereference bool, isRoot bool, ancestors map[string]struct{}) error {
	fInfo, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
	}

	// Use the attributes of the dereferenced file when following symlinks.
	if dereference && fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
		fInfo, err = os.Stat(p)
		if err != nil {
			return err
		}
	}

	// Detect unsupported files
	if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
		return fmt.Errorf(i18n.G("'%s' isn't a supported file type"), p)
	}

	// Prepare for file transfer
	fileArgs := incus.InstanceFileArgs{
		UID:  args.UID,
		GID:  args.GID,
		Mode: -1,
	}

	// --mode only applies to the root of the transfer.
	if isRoot {
		fileArgs.Mode = args.Mode
	}

	var readCloser io.ReadCloser

	if fInfo.IsDir() {
		// Directory handling
		fileArgs.Type = "directory"
	} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
		// Symlink handling
		symlinkTarget, err := os.Readlink(p)
		if err != nil {
			return err
		}

		fileArgs.Type = "symlink"
		fileArgs.Content = strings.NewReader(symlinkTarget)
		readCloser = io.NopCloser(fileArgs.Content)
	} else {
		// File handling
		f, err := os.Open(p)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to open source file %q: %v"), p, err)
		}

		defer logger.WarnOnError(f.Close, "Failed to close file")

		fileArgs.Type = "file"
		fileArgs.Content = f
		readCloser = f
	}

	progress := cli.ProgressRenderer{
		Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), p, targetPath),
		Quiet:  quiet,
	}

	if fileArgs.Type != "directory" {
		contentLength, err := fileArgs.Content.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}

		_, err = fileArgs.Content.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		fileArgs.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
			ReadCloser: readCloser,
			Tracker: &ioprogress.ProgressTracker{
				Length: contentLength,
				Handler: func(percent int64, speed int64) {
					progress.UpdateProgress(ioprogress.ProgressData{
						Text: fmt.Sprintf("%d%% (%s/s)", percent,
							units.GetByteSizeString(speed, 2)),
					})
				},
			},
		}, fileArgs.Content)
	}

	// Existing files keep their ownership and mode unless requested otherwise.
	_, err = sftpConn.Lstat(targetPath)
	sftpPushArgs(&fileArgs, fInfo, archive, err == nil)

	logger.Infof("Pushing %s to %s (%s)", p, targetPath, fileArgs.Type)
	err = sftpCreateFile(sftpConn, targetPath, fileArgs, true)
	if fileArgs.Type != "directory" {
		progress.Done("")
	}

	if err != nil {
		return err
	}

	if fInfo.IsDir() {
		// Detect symlink loops when dereferencing.
		if dereference {
			realPath, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}

			_, seen := ancestors[realPath]
			if seen {
				return fmt.Errorf(i18n.G("Cyclic symbolic link %q"), p)
			}

			ancestors[realPath] = struct{}{}
			defer delete(ancestors, realPath)
		}

		entries, err := os.ReadDir(p)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		for _, ent := range entries {
			err = sftpRecursivePushEntry(sftpConn, filepath.Join(p, ent.Name()), path.Join(targetPath, ent.Name()), args, quiet, archive, dereference, false, ancestors)
			if err != nil {
				return err
			}
		}
	}

	// Timestamps are set last so they're not changed by the directory content.
	if archive && fileArgs.Type != "symlink" {
		sftpSetRemoteTimes(sftpConn, targetPath, fInfo)
	}

	return nil
}

func sftpRecursiveMkdir(sftpConn *sftp.Client, p string, mode *os.FileMode, uid int64, gid int64) error {
	/* special case, every instance has a /, we don't need to do anything */
	if p == "/" {
		return nil
	}

	// Remove trailing "/" e.g. /A/B/C/. Otherwise we will end up with an
	// empty array entry "" which will confuse the Mkdir() loop below.
	pclean := path.Clean(p)
	parts := strings.Split(pclean, "/")
	i := len(parts)

	for ; i >= 1; i-- {
		cur := path.Join(parts[:i]...)
		fInfo, err := sftpConn.Lstat(cur)
		if err != nil {
			continue
		}

		if !fInfo.IsDir() {
			return fmt.Errorf(i18n.G("%s is not a directory"), cur)
		}

		i++
		break
	}

	for ; i <= len(parts); i++ {
		cur := path.Join(parts[:i]...)
		if cur == "" {
			continue
		}

		cur = "/" + cur
		cur = strings.TrimLeft(cur, "/")

		modeArg := -1
		if mode != nil {
			modeArg = int(mode.Perm())
		}

		args := incus.InstanceFileArgs{
			UID:  max(uid, 0),
			GID:  max(gid, 0),
			Mode: modeArg,
			Type: "directory",
		}

		logger.Infof("Creating %s (%s)", cur, args.Type)
		err := sftpCreateFile(sftpConn, cur, args, false)
		if err != nil {
			return err
		}
	}

	return nil
}
