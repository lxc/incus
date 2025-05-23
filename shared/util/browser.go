package util

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the provided URL in the default web browser.
func OpenBrowser(url string) error {
	var err error

	browser := os.Getenv("BROWSER")
	if browser != "" {
		if browser == "none" {
			return nil
		}

		err = exec.Command(browser, url).Start()
		return err
	}

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = errors.New("unsupported platform")
	}

	if err != nil {
		return err
	}

	return nil
}
