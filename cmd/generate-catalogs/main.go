// Command generate-catalogs writes the translated entries of the PO files as compressed catalogs for embedding.
package main

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v7/internal/i18n/catalog"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: generate-catalogs PO-DIRECTORY OUTPUT-DIRECTORY")
		os.Exit(1)
	}

	err := run(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// run replaces the catalogs of the output directory with those of the PO files holding translations.
func run(srcDir string, dstDir string) error {
	stale, err := filepath.Glob(filepath.Join(dstDir, "*.po.gz"))
	if err != nil {
		return err
	}

	for _, path := range stale {
		err = os.Remove(path)
		if err != nil {
			return err
		}
	}

	files, err := filepath.Glob(filepath.Join(srcDir, "*.po"))
	if err != nil {
		return err
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		translations := catalog.Parse(string(data))
		if len(translations) == 0 {
			continue
		}

		name := strings.TrimSuffix(filepath.Base(path), ".po")
		err = writeCatalog(filepath.Join(dstDir, name+".po.gz"), translations)
		if err != nil {
			return err
		}
	}

	return nil
}

// writeCatalog writes the translations as a gzip compressed catalog.
func writeCatalog(path string, translations map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		_ = f.Close()
		return err
	}

	err = catalog.Write(zw, translations)
	if err != nil {
		_ = f.Close()
		return err
	}

	err = zw.Close()
	if err != nil {
		_ = f.Close()
		return err
	}

	return f.Close()
}
