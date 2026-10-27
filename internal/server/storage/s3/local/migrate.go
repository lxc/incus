package local

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/lxc/incus/v7/shared/logger"
)

// minioSubdir is the legacy directory containing data managed by an embedded
// minio process.
const minioSubdir = "minio"

// minioArchivedSubdir is the post-migration name of the legacy directory.
const minioArchivedSubdir = ".minio"

// minioMetadataDir is minio's per-bucket / global metadata directory.
const minioMetadataDir = ".minio.sys"

// minioMetaFile is the per-object metadata file written by minio's modern
// (xl-storage) backend. Its presence in a directory marks the directory as
// an object store, not a path component.
const minioMetaFile = "xl.meta"

// migrateLocks serialises migrations per bucket directory.
var migrateLocks sync.Map // map[string]*sync.Mutex

// MigrateMinioBucket converts a bucket directory previously managed by an
// embedded minio process to the data/ layout used by Server.
//
// On success the bucket directory contains:
//
//	data/      object data (one regular file per object key)
//	.minio/    everything left over from minio (metadata, format files)
//
// Objects in minio's xl-storage layout each live in a directory named after
// the object key. That directory contains an xl.meta file, and either holds
// the object data inline (small objects) or alongside a UUID-named
// subdirectory containing part files (large / multipart objects). The
// migration converts each such object directory into a single regular file
// at data/<key>.
//
// The function is a no-op when minio/ does not exist (already migrated, or
// freshly created bucket).
func MigrateMinioBucket(bucketDir, bucketName string) error {
	lockI, _ := migrateLocks.LoadOrStore(bucketDir, &sync.Mutex{})
	lock, ok := lockI.(*sync.Mutex)
	if ok {
		lock.Lock()
		defer lock.Unlock()
	}

	src := filepath.Join(bucketDir, minioSubdir)

	_, err := os.Stat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	dst := filepath.Join(bucketDir, minioArchivedSubdir)
	_, err = os.Stat(dst)
	if err == nil {
		return errors.New("Both minio/ and .minio/ are present; migration cannot proceed")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	dataDir := filepath.Join(bucketDir, dataSubdir)
	err = os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return err
	}

	l := logger.AddContext(logger.Ctx{"bucket": bucketName, "path": bucketDir})
	l.Info("Migrating storage bucket from the legacy minio layout")

	bucketRoot := filepath.Join(src, bucketName)

	_, err = os.Stat(bucketRoot)
	if err == nil {
		err = walkAndConvert(bucketRoot, dataDir)
		if err != nil {
			l.Error("Failed migrating storage bucket from the legacy minio layout", logger.Ctx{"err": err})
			return fmt.Errorf("Failed migrating minio bucket data: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	err = os.Rename(src, dst)
	if err != nil {
		return fmt.Errorf("Failed archiving minio directory: %w", err)
	}

	l.Info("Migrated storage bucket from the legacy minio layout")

	return nil
}

// walkAndConvert traverses an xl-storage tree under src, writing each object
// it finds as a regular file under dst. minio's metadata directories are
// left in place (the source tree is preserved alongside; only data files
// are extracted).
func walkAndConvert(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !d.IsDir() {
			return nil
		}

		// Don't descend into metadata dirs.
		if d.Name() == minioMetadataDir {
			return filepath.SkipDir
		}

		// Object directories contain xl.meta. Convert them and don't
		// recurse further (the part-data subdirs underneath are the
		// object's storage, not nested objects).
		if hasFile(path, minioMetaFile) {
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}

			target := filepath.Join(dst, rel)

			// Objects extracted by an earlier interrupted pass are complete.
			_, err = os.Stat(target)
			if err == nil {
				return filepath.SkipDir
			}

			err = os.MkdirAll(filepath.Dir(target), 0o700)
			if err != nil {
				return err
			}

			err = extractObjectDir(path, target)
			if err != nil {
				return fmt.Errorf("extract %q: %w", rel, err)
			}

			return filepath.SkipDir
		}

		return nil
	})
}

// extractObjectDir reads the xl.meta inside srcDir and writes the assembled
// object data to dst as a regular file.
//
// Small objects have their data stored inline in xl.meta and are extracted
// from there. Larger objects have part files in a UUID-named subdirectory of
// srcDir; those are concatenated in part-number order.
func extractObjectDir(srcDir, dst string) error {
	metaBytes, err := os.ReadFile(filepath.Join(srcDir, minioMetaFile))
	if err != nil {
		return err
	}

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	cleanup := func() {
		_ = out.Close()
		_ = os.Remove(tmp)
	}

	inline, err := xlInlineData(metaBytes)
	if err == nil && inline != nil {
		err = stripBitrotHashes(out, bytes.NewReader(inline))
		if err != nil {
			cleanup()
			return err
		}
	} else {
		// External data: find the part-file subdirectory (the only
		// non-xl.meta directory inside srcDir) and concatenate its
		// part.N files.
		partsDir, err := findPartsDir(srcDir)
		if err != nil {
			cleanup()
			return err
		}

		err = concatParts(partsDir, out)
		if err != nil {
			cleanup()
			return err
		}
	}

	err = out.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dst)
}

// findPartsDir returns the path to the single subdirectory under objDir
// (other than minio's own metadata directories) that holds the object's
// part files.
func findPartsDir(objDir string) (string, error) {
	entries, err := os.ReadDir(objDir)
	if err != nil {
		return "", err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if e.Name() == minioMetadataDir {
			continue
		}

		return filepath.Join(objDir, e.Name()), nil
	}

	return "", fmt.Errorf("no parts directory under %q", objDir)
}

// concatParts writes the concatenation of partsDir/part.<N> files (sorted
// numerically by N) to w.
func concatParts(partsDir string, w io.Writer) error {
	entries, err := os.ReadDir(partsDir)
	if err != nil {
		return err
	}

	type part struct {
		path string
		num  int
	}

	parts := make([]part, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		nstr, ok := strings.CutPrefix(name, "part.")
		if !ok {
			continue
		}

		n, err := strconv.Atoi(nstr)
		if err != nil {
			continue
		}

		parts = append(parts, part{path: filepath.Join(partsDir, name), num: n})
	}

	if len(parts) == 0 {
		return fmt.Errorf("no part files in %q", partsDir)
	}

	sort.Slice(parts, func(i, j int) bool { return parts[i].num < parts[j].num })

	for _, p := range parts {
		f, err := os.Open(p.path)
		if err != nil {
			return err
		}

		err = stripBitrotHashes(w, f)
		_ = f.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// minio bitrot protection prepends a 32-byte HighwayHash256 hash to every
// 1MB block of object data. Both inline data and part files use this
// layout. The migration extracts the actual bytes by skipping each hash.
const (
	bitrotHashSize  = 32
	bitrotBlockSize = 1 << 20
)

// stripBitrotHashes copies r to w, dropping the leading bitrotHashSize
// bytes of every bitrotBlockSize-byte chunk. The final chunk may be
// shorter than bitrotBlockSize; its trailing data is still copied.
func stripBitrotHashes(w io.Writer, r io.Reader) error {
	buf := make([]byte, bitrotBlockSize)

	for {
		// Read and discard the per-block hash.
		_, err := io.ReadFull(r, buf[:bitrotHashSize])
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		// Read up to one block worth of data.
		n, err := io.ReadFull(r, buf[:bitrotBlockSize])
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
		}

		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

// hasFile reports whether dir contains a regular file with the given name.
func hasFile(dir, name string) bool {
	st, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return false
	}

	return !st.IsDir()
}
