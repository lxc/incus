package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lxc/incus/v6/shared/revert"
)

// TextEditor spawns the editor with a temporary YAML file for editing configs.
func TextEditor(inPath string, inContent []byte) ([]byte, error) {
	var f *os.File
	var err error
	var path string

	// Detect the text editor to use
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
		if editor == "" {
			for _, p := range []string{"editor", "vi", "emacs", "nano"} {
				_, err := exec.LookPath(p)
				if err == nil {
					editor = p
					break
				}
			}
			if editor == "" {
				return []byte{}, errors.New("No text editor found, please set the EDITOR environment variable")
			}
		}
	}

	if inPath == "" {
		// If provided input, create a new file
		f, err = os.CreateTemp("", "editor_")
		if err != nil {
			return []byte{}, err
		}

		reverter := revert.New()
		defer reverter.Fail()

		reverter.Add(func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		})

		err = os.Chmod(f.Name(), 0o600)
		if err != nil {
			return []byte{}, err
		}

		_, err = f.Write(inContent)
		if err != nil {
			return []byte{}, err
		}

		err = f.Close()
		if err != nil {
			return []byte{}, err
		}

		path = fmt.Sprintf("%s.yaml", f.Name())
		err = os.Rename(f.Name(), path)
		if err != nil {
			return []byte{}, err
		}

		reverter.Success()
		reverter.Add(func() { _ = os.Remove(path) })
	} else {
		path = inPath
	}

	cmdParts := strings.Fields(editor)
	cmd := exec.Command(cmdParts[0], append(cmdParts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return []byte{}, err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}
