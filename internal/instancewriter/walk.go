//go:build linux && cgo

package instancewriter

import (
	"os"
	"path/filepath"
	"sort"
)

// Walk walks the file tree rooted at root like filepath.Walk, but confined to root: a path
// component that is a symlink pointing outside root is not followed, so an entry swapped for a
// symlink mid-walk can't send the caller into the rest of the host filesystem. The callback is
// given the absolute path and an lstat result taken within root.
func Walk(root string, fn filepath.WalkFunc) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}

	defer func() { _ = r.Close() }()

	return walk(r, root, ".", fn)
}

func walk(r *os.Root, root string, rel string, fn filepath.WalkFunc) error {
	path := root
	if rel != "." {
		path = filepath.Join(root, rel)
	}

	fi, err := r.Lstat(rel)
	if err != nil {
		return fn(path, nil, err)
	}

	err = fn(path, fi, nil)
	if err != nil {
		if fi.IsDir() && err == filepath.SkipDir {
			return nil
		}

		return err
	}

	if !fi.IsDir() {
		return nil
	}

	names, err := readDirNames(r, rel)
	if err != nil {
		return fn(path, fi, err)
	}

	for _, name := range names {
		child := name
		if rel != "." {
			child = rel + "/" + name
		}

		err = walk(r, root, child, fn)
		if err != nil {
			if err == filepath.SkipDir {
				break
			}

			return err
		}
	}

	return nil
}

// readDirNames lists a directory within the root. It uses Readdirnames rather than fs.WalkDir so
// that directory names which aren't valid UTF-8 don't abort the walk.
func readDirNames(r *os.Root, rel string) ([]string, error) {
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}

	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return nil, err
	}

	sort.Strings(names)

	return names, nil
}
