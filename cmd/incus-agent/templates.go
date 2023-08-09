package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
)

func templatesApply(path string) ([]string, error) {
	metaName := filepath.Join(path, "metadata.yaml")
	if !shared.PathExists(metaName) {
		return nil, nil
	}

	// Parse the metadata.
	content, err := os.ReadFile(metaName)
	if err != nil {
		return nil, fmt.Errorf("Failed to read metadata: %w", err)
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)
	if err != nil {
		return nil, fmt.Errorf("Could not parse metadata.yaml: %w", err)
	}

	// Go through the files and copy them into place.
	files := []string{}
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			filePath := filepath.Join(path, fmt.Sprintf("%s.out", tpl.Template))

			if !shared.PathExists(filePath) {
				return nil
			}

			var w *os.File
			if shared.PathExists(tplPath) {
				if tpl.CreateOnly {
					return nil
				}

				// Open the existing file.
				w, err = os.Create(tplPath)
				if err != nil {
					return fmt.Errorf("Failed to create template file: %w", err)
				}
			} else {
				// Create the directories leading to the file.
				err := os.MkdirAll(filepath.Dir(tplPath), 0755)
				if err != nil {
					return err
				}

				// Create the file itself.
				w, err = os.Create(tplPath)
				if err != nil {
					return err
				}

				// Fix mode.
				err = w.Chmod(0644)
				if err != nil {
					return err
				}
			}
			defer func() { _ = w.Close() }()

			// Do the copy.
			src, err := os.Open(filePath)
			if err != nil {
				return err
			}

			defer func() { _ = src.Close() }()

			_, err = io.Copy(w, src)
			if err != nil {
				return err
			}

			err = w.Close()
			if err != nil {
				return err
			}

			files = append(files, tplPath)

			return nil
		}(tplPath, tpl)

		if err != nil {
			return nil, err
		}
	}

	return files, nil
}
