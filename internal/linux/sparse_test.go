package linux

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSparseFileWrapper(t *testing.T) {
	// Build a buffer of random blocks separated by long zero runs, including a partial trailing block.
	// The runs are kept well above any filesystem allocation unit (64KiB pages on some architectures)
	// so that they can be stored as holes everywhere.
	src := make([]byte, 0, 4*65*sparseBlockSize+100)
	for i := range 4 * 65 {
		block := make([]byte, sparseBlockSize)
		if i%65 == 0 {
			_, _ = rand.Read(block)
		}

		src = append(src, block...)
	}

	src = append(src, bytes.Repeat([]byte{1}, 100)...)

	path := filepath.Join(t.TempDir(), "sparse")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = io.CopyBuffer(NewSparseFileWrapper(f), bytes.NewReader(src), make([]byte, 3*sparseBlockSize+7))
	if err != nil {
		t.Fatal(err)
	}

	err = f.Truncate(int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}

	_ = f.Close()

	dst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(src, dst) {
		t.Fatalf("Content mismatch")
	}

	// Check that zero-filled blocks were left as holes.
	var st unix.Stat_t
	err = unix.Stat(path, &st)
	if err != nil {
		t.Fatal(err)
	}

	if st.Blocks*512 >= int64(len(src)) {
		t.Fatalf("File is not sparse: %d blocks", st.Blocks)
	}
}
