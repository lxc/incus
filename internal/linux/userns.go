package linux

import (
	"bufio"
	"fmt"
	"os"
)

func RunningInUserNS() bool {
	file, err := os.Open("/proc/self/uid_map")
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	buf := bufio.NewReader(file)
	l, _, err := buf.ReadLine()
	if err != nil {
		return false
	}

	line := string(l)
	var a, b, c int64
	_, _ = fmt.Sscanf(line, "%d %d %d", &a, &b, &c)
	if a == 0 && b == 0 && c == 4294967295 {
		return false
	}

	return true
}
