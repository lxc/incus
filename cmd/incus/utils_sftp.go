package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
			return err
		}
	}

	// Set mode.
	if args.Mode >= 0 {
		err = sftpConn.Chmod(targetPath, fs.FileMode(args.Mode))
		if err != nil {
			return err
		}
	}

	return nil
}

func sftpCreateFile(sftpConn *sftp.Client, targetPath string, args incus.InstanceFileArgs, push bool) error {
	switch args.Type {
	case "file":
		file, err := sftpConn.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to open target file %q: %w"), targetPath, err)
		}

		defer func() { _ = file.Close() }()

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

func sftpRecursivePullFile(sftpConn *sftp.Client, fInfo os.FileInfo, source string, normalizedSource string, targetDir string, quiet bool, dereference bool, createRoot bool) error {
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
		root := filepath.Base(source)
		// `cp` has a special behavior with the following paths.
		if root == "." || root == ".." {
			root = ""
		}

		target = filepath.Join(targetDir, root)
	}

	logger.Infof("Pulling %s from %s (%s)", target, normalizedSource, fileType)

	switch fileType {
	case "directory":
		err := os.Mkdir(target, fInfo.Mode())
		if err != nil {
			// If the error isn’t that the path already exists, there’s nothing we can do about it.
			if !errors.Is(err, os.ErrExist) {
				return err
			}

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
			nextP := filepath.Join(normalizedSource, ent.Name())
			stat := sftpConn.Lstat
			if dereference {
				stat = sftpConn.Stat
			}

			nextInfo, err := stat(nextP)
			if err != nil {
				return err
			}

			err = sftpRecursivePullFile(sftpConn, nextInfo, nextP, nextP, target, quiet, dereference, true)
			if err != nil {
				return err
			}
		}
	case "file":
		src, err := sftpConn.Open(normalizedSource)
		if err != nil {
			return err
		}

		defer func() { _ = src.Close() }()

		dst, err := os.Create(target)
		if err != nil {
			return err
		}

		defer func() { _ = dst.Close() }()

		err = os.Chmod(target, fInfo.Mode())
		if err != nil {
			return err
		}

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

	default:
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), fileType)
	}

	return nil
}

func sftpRecursivePushFile(sftpConn *sftp.Client, walkableSource string, source string, target string, args incus.InstanceFileArgs, quiet bool, dereference bool, createRoot bool) error {
	root := ""
	if createRoot {
		root = filepath.Base(source)
		// `cp` has a special behavior with the following paths.
		if root == "." || root == ".." {
			root = ""
		}
	}

	isRoot := true
	sendFile := func(p string, fInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		// Detect unsupported files
		if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
			return fmt.Errorf(i18n.G("'%s' isn't a supported file type"), p)
		}

		// Prepare for file transfer
		targetPath := filepath.Join(target, root, p[len(walkableSource):])
		mode, uid, gid := internalIO.GetOwnerMode(fInfo)
		fileArgs := incus.InstanceFileArgs{
			UID:  int64(uid),
			GID:  int64(gid),
			Mode: int(mode.Perm()),
		}

		if args.UID != -1 {
			fileArgs.UID = args.UID
		}

		if args.GID != -1 {
			fileArgs.GID = args.GID
		}

		if isRoot {
			isRoot = false
			if args.Mode != -1 {
				fileArgs.Mode = args.Mode
			}
		}

		var readCloser io.ReadCloser

		if fInfo.IsDir() {
			// Directory handling
			fileArgs.Type = "directory"
		} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink && !dereference {
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

			defer func() { _ = f.Close() }()

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

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, fileArgs.Type)
		err = sftpCreateFile(sftpConn, targetPath, fileArgs, true)
		if err != nil {
			if fileArgs.Type != "directory" {
				progress.Done("")
			}

			return err
		}

		if fileArgs.Type != "directory" {
			progress.Done("")
		}

		return nil
	}

	return filepath.Walk(walkableSource, sendFile)
}

func sftpRecursiveMkdir(sftpConn *sftp.Client, p string, mode *os.FileMode, uid int64, gid int64) error {
	/* special case, every instance has a /, we don't need to do anything */
	if p == "/" {
		return nil
	}

	// Remove trailing "/" e.g. /A/B/C/. Otherwise we will end up with an
	// empty array entry "" which will confuse the Mkdir() loop below.
	pclean := filepath.Clean(p)
	parts := strings.Split(pclean, "/")
	i := len(parts)

	for ; i >= 1; i-- {
		cur := filepath.Join(parts[:i]...)
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
		cur := filepath.Join(parts[:i]...)
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
