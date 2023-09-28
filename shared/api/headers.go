package api

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
)

// ParseFileHeaders extracts the file ownership, type, mode and operation type from HTTP headers.
func ParseFileHeaders(headers http.Header) (int64, int64, int, string, string) {
	getHeader := func(key string) string {
		value := headers.Get(fmt.Sprintf("X-Incus-%s", key))
		if value == "" {
			// Legacy support.
			value = headers.Get(fmt.Sprintf("X-LXD-%s", key))
		}

		return value
	}

	uid, err := strconv.ParseInt(getHeader("uid"), 10, 64)
	if err != nil {
		uid = -1
	}

	gid, err := strconv.ParseInt(getHeader("gid"), 10, 64)
	if err != nil {
		gid = -1
	}

	mode, err := strconv.Atoi(getHeader("mode"))
	if err != nil {
		mode = -1
	} else {
		rawMode, err := strconv.ParseInt(getHeader("mode"), 0, 0)
		if err == nil {
			mode = int(os.FileMode(rawMode) & os.ModePerm)
		}
	}

	fileType := getHeader("type")
	if fileType == "" {
		// Default is standard file.
		fileType = "file"
	}

	writeMode := getHeader("write")
	if writeMode == "" {
		// Default is to override the content.
		writeMode = "overwrite"
	}

	return uid, gid, mode, fileType, writeMode
}
