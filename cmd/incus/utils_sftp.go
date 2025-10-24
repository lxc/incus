package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	internalIO "github.com/lxc/incus/v6/internal/io"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/units"
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
			return err
		}

		defer func() { _ = file.Close() }()

		if push {
			for {
				// Read 1MB at a time.
				_, err = io.CopyN(file, args.Content, 1024*1024)
				if err != nil {
					if err == io.EOF {
						break
					}

					return err
				}
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

func sftpRecursivePullFile(sftpConn *sftp.Client, p string, targetDir string, quiet bool) error {
	fInfo, err := sftpConn.Lstat(p)
	if err != nil {
		return err
	}

	var fileType string
	if fInfo.IsDir() {
		fileType = "directory"
	} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
		fileType = "symlink"
	} else {
		fileType = "file"
	}

	target := filepath.Join(targetDir, filepath.Base(p))
	logger.Infof("Pulling %s from %s (%s)", target, p, fileType)

	if fileType == "directory" {
		err := os.Mkdir(target, fInfo.Mode())
		if err != nil {
			return err
		}

		entries, err := sftpConn.ReadDir(p)
		if err != nil {
			return err
		}

		for _, ent := range entries {
			nextP := filepath.Join(p, ent.Name())

			err := sftpRecursivePullFile(sftpConn, nextP, target, quiet)
			if err != nil {
				return err
			}
		}
	} else if fileType == "file" {
		src, err := sftpConn.Open(p)
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
			Format: fmt.Sprintf(i18n.G("Pulling %s from %s: %%s"), p, target),
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

		for {
			// Read 1MB at a time.
			_, err = io.CopyN(writer, src, 1024*1024)
			if err != nil {
				if err == io.EOF {
					break
				}

				progress.Done("")
				return err
			}
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
	} else if fileType == "symlink" {
		linkTarget, err := sftpConn.ReadLink(p)
		if err != nil {
			return err
		}

		err = os.Symlink(linkTarget, target)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf(i18n.G("Unknown file type '%s'"), fileType)
	}

	return nil
}

func sftpRecursivePushFile(sftpConn *sftp.Client, source string, target string, quiet bool) error {
	source = filepath.Clean(source)

	sourceDir, _ := filepath.Split(source)
	sourceLen := len(sourceDir)

	// Special handling for relative paths.
	if source == ".." {
		sourceLen = 1
	}

	sendFile := func(p string, fInfo os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to walk path for %s: %s"), p, err)
		}

		// Detect unsupported files
		if !fInfo.Mode().IsRegular() && !fInfo.Mode().IsDir() && fInfo.Mode()&os.ModeSymlink != os.ModeSymlink {
			return fmt.Errorf(i18n.G("'%s' isn't a supported file type"), p)
		}

		// Prepare for file transfer
		targetPath := filepath.Join(target, filepath.ToSlash(p[sourceLen:]))
		mode, uid, gid := internalIO.GetOwnerMode(fInfo)
		args := incus.InstanceFileArgs{
			UID:  int64(uid),
			GID:  int64(gid),
			Mode: int(mode.Perm()),
		}

		var readCloser io.ReadCloser

		if fInfo.IsDir() {
			// Directory handling
			args.Type = "directory"
		} else if fInfo.Mode()&os.ModeSymlink == os.ModeSymlink {
			// Symlink handling
			symlinkTarget, err := os.Readlink(p)
			if err != nil {
				return err
			}

			args.Type = "symlink"
			args.Content = bytes.NewReader([]byte(symlinkTarget))
			readCloser = io.NopCloser(args.Content)
		} else {
			// File handling
			f, err := os.Open(p)
			if err != nil {
				return err
			}

			defer func() { _ = f.Close() }()

			args.Type = "file"
			args.Content = f
			readCloser = f
		}

		progress := cli.ProgressRenderer{
			Format: fmt.Sprintf(i18n.G("Pushing %s to %s: %%s"), p, targetPath),
			Quiet:  quiet,
		}

		if args.Type != "directory" {
			contentLength, err := args.Content.Seek(0, io.SeekEnd)
			if err != nil {
				return err
			}

			_, err = args.Content.Seek(0, io.SeekStart)
			if err != nil {
				return err
			}

			args.Content = internalIO.NewReadSeeker(&ioprogress.ProgressReader{
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
			}, args.Content)
		}

		logger.Infof("Pushing %s to %s (%s)", p, targetPath, args.Type)
		err = sftpCreateFile(sftpConn, targetPath, args, true)
		if err != nil {
			if args.Type != "directory" {
				progress.Done("")
			}

			return err
		}

		if args.Type != "directory" {
			progress.Done("")
		}

		return nil
	}

	return filepath.Walk(source, sendFile)
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
			UID:  uid,
			GID:  gid,
			Mode: modeArg,
			Type: "directory",
		}

		logger.Errorf("Creating %s (%s)", cur, args.Type)
		err := sftpCreateFile(sftpConn, cur, args, false)
		if err != nil {
			return err
		}
	}

	return nil
}
