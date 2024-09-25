package apparmor

import (
	"bytes"
	"fmt"
	"testing"
)

func TestHandleWriter(t *testing.T) {
	status := []int64{}
	var buffer bytes.Buffer
	out := &nullWriteCloser{handleWriter(&buffer, func(percent int64, _ int64) {
		status = append(status, percent)
	})}

	for i := 0; i < 101; i++ {
		for j := 0; j < 100; j++ {
			n, err := fmt.Fprintf(out, "\t    (%02d.%02d/100%s)\r", i, j, "%")
			if err != nil {
				t.Fatal(err, n)
			}

			if i == 100 {
				break
			}
		}
	}

	if len(status) != 100 {
		t.Fatal(status)
	}

	for i := int64(1); i < 101; i++ {
		if status[i-1] != i {
			t.Fatal(status[i], i)
		}
	}

	output := buffer.String()
	// Do not check output carefully
	if len(output) == 0 {
		t.Fatal(output)
	}
}
