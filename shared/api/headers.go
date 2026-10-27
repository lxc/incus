package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/lxc/incus/v7/shared/util"
)

// ParseFileHeaders extracts the file ownership, type, mode and operation type from HTTP headers.
func ParseFileHeaders(headers http.Header) (int64, int64, int, string, string) {
	getHeader := func(key string) string {
		return headers.Get(fmt.Sprintf("X-Incus-%s", key))
	}

	uid, err := strconv.ParseInt(getHeader("uid"), 10, 64)
	if err != nil {
		uid = -1
	}

	gid, err := strconv.ParseInt(getHeader("gid"), 10, 64)
	if err != nil {
		gid = -1
	}

	mode := -1
	m, err := util.ParseMode(getHeader("mode"))
	if err == nil {
		mode = int(m)
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
