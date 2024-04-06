package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

func templatesApply(path string) ([]string, error) {
	metaName := filepath.Join(path, "metadata.yaml")
	if !util.PathExists(metaName) {
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

			if !util.PathExists(filePath) {
				return nil
			}

			var w *os.File
			if util.PathExists(tplPath) {
				if tpl.CreateOnly {
					return nil
				}

				// Open the existing file.
				w, err = os.Create(tplPath)
				if err != nil {
					return fmt.Errorf("Failed to create template file: %w", err)
				}
			} else {
				// UID and GID
				fileUID := int64(0)
				fileGID := int64(0)

				if tpl.UID != "" {
					id, err := strconv.ParseInt(tpl.UID, 10, 64)
					if err != nil {
						return fmt.Errorf("Bad file UID %q for %q: %w", tpl.UID, tplPath, err)
					}

					fileUID = id
				}

				if tpl.GID != "" {
					id, err := strconv.ParseInt(tpl.GID, 10, 64)
					if err != nil {
						return fmt.Errorf("Bad file GID %q for %q: %w", tpl.GID, tplPath, err)
					}

					fileGID = id
				}

				// Mode
				fileMode := fs.FileMode(0644)
				if tpl.Mode != "" {
					if len(tpl.Mode) == 3 {
						tpl.Mode = fmt.Sprintf("0%s", tpl.Mode)
					}

					mode, err := strconv.ParseInt(tpl.Mode, 0, 0)
					if err != nil {
						return fmt.Errorf("Bad mode %q for %q: %w", tpl.Mode, tplPath, err)
					}

					fileMode = os.FileMode(mode) & os.ModePerm
				}

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

				// Fix ownership.
				err = w.Chown(int(fileUID), int(fileGID))
				if err != nil {
					return err
				}

				// Fix mode.
				err = w.Chmod(fileMode)
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
