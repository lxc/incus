package linux

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lxc/incus/v6/shared/subprocess"
)

// ClearBlock fully resets a block device or disk file using the most efficient mechanism available.
// For files, it will truncate them down to zero and back to their original size.
// For blocks, it will attempt a variety of discard options, validating the result with marker files and eventually fallback to full zero-ing.
//
// An offset can be specified to only reset a part of a device.
func ClearBlock(blockPath string, blockOffset int64) error {
	// Open the block device for checking.
	fd, err := os.OpenFile(blockPath, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			// If the file is missing, there is nothing to clear.
			return nil
		}

		return err
	}

	defer fd.Close()

	// Get the size of the file/block.
	size, err := fd.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	// Get all the stat data.
	st, err := fd.Stat()
	if err != nil {
		return err
	}

	if !IsBlockdev(st.Mode()) {
		// For files, truncate them.
		err := fd.Truncate(blockOffset)
		if err != nil {
			return err
		}

		err = fd.Truncate(size)
		if err != nil {
			return err
		}

		return nil
	}

	// Blocks are trickier to reset with options varying based on disk features.
	// We use a set of 3 markers to validate whether it was reset.
	marker := []byte("INCUS")
	markerLength := int64(len(marker))
	markerOffsetStart := blockOffset
	markerOffsetMiddle := blockOffset + ((size - blockOffset) / 2)
	markerOffsetEnd := size - markerLength

	if markerOffsetStart+markerLength > size {
		// No markers can fit.
		markerOffsetStart = -1
		markerOffsetMiddle = -1
		markerOffsetEnd = -1
	} else {
		if markerOffsetMiddle <= markerOffsetStart+markerLength {
			// Middle marker goes over start marker.
			markerOffsetMiddle = -1
		}

		if markerOffsetEnd <= markerOffsetMiddle+markerLength {
			// End marker goes over middle marker.
			markerOffsetEnd = -1
		}

		if markerOffsetEnd <= markerOffsetStart+markerLength {
			// End marker goes over start marker.
			markerOffsetEnd = -1
		}
	}

	writeMarkers := func(fd *os.File) error {
		for _, offset := range []int64{markerOffsetStart, markerOffsetMiddle, markerOffsetEnd} {
			if offset < 0 {
				continue
			}

			// Write the marker at the set offset.
			n, err := fd.WriteAt(marker, offset)
			if err != nil {
				return err
			}

			if n != int(markerLength) {
				return fmt.Errorf("Only managed to write %d bytes out of %d of the %d offset marker", n, markerLength, offset)
			}
		}

		return nil
	}

	checkMarkers := func(fd *os.File) (int, error) {
		found := 0

		for _, offset := range []int64{markerOffsetStart, markerOffsetMiddle, markerOffsetEnd} {
			if offset < 0 {
				found++
				continue
			}

			buf := make([]byte, markerLength)

			// Read the marker from the offset.
			n, err := fd.ReadAt(buf, offset)
			if err != nil {
				return found, err
			}

			if n != int(markerLength) {
				return found, fmt.Errorf("Only managed to read %d bytes out of %d of the %d offset marker", n, markerLength, offset)
			}

			// Check if we found it.
			if string(buf) == string(marker) {
				found++
			}
		}

		return found, nil
	}

	// Write and check an initial set of markers.
	err = writeMarkers(fd)
	if err != nil {
		return err
	}

	found, err := checkMarkers(fd)
	if err != nil {
		return err
	}

	if found != 3 {
		return errors.New("Some of our initial markers weren't written properly")
	}

	// Start clearing the block.
	_ = fd.Close()

	// Attempt a secure discard run.
	_, err = subprocess.RunCommand("blkdiscard", "-f", "-o", fmt.Sprintf("%d", blockOffset), "-s", blockPath)
	if err == nil {
		// Check if the markers are gone.
		fd, err := os.Open(blockPath)
		if err != nil {
			return err
		}

		defer fd.Close()

		found, err = checkMarkers(fd)
		if err != nil {
			return err
		}

		if found == 0 {
			// All markers are gone, secure discard succeeded.
			return nil
		}

		// Some markers were found, go to the next clearing option.
		_ = fd.Close()
	}

	// Attempt a regular discard run.
	_, err = subprocess.RunCommand("blkdiscard", "-f", "-o", fmt.Sprintf("%d", blockOffset), blockPath)
	if err == nil {
		// Check if the markers are gone.
		fd, err := os.Open(blockPath)
		if err != nil {
			return err
		}

		defer fd.Close()

		found, err = checkMarkers(fd)
		if err != nil {
			return err
		}

		if found == 0 {
			// All markers are gone, regular discard succeeded.
			return nil
		}

		// Some markers were found, go to the next clearing option.
		_ = fd.Close()
	}

	// Attempt device zero-ing.
	_, err = subprocess.RunCommand("blkdiscard", "-f", "-o", fmt.Sprintf("%d", blockOffset), "-z", blockPath)
	if err == nil {
		// Check if the markers are gone.
		fd, err := os.Open(blockPath)
		if err != nil {
			return err
		}

		defer fd.Close()

		found, err = checkMarkers(fd)
		if err != nil {
			return err
		}

		if found == 0 {
			// All markers are gone, device zero-ing succeeded.
			return nil
		}

		// Some markers were found, go to the next clearing option.
		_ = fd.Close()
	}

	// All fast discard attempts have failed, proceed with manual zero-ing.
	zero, err := os.Open("/dev/zero")
	if err != nil {
		return err
	}

	defer zero.Close()

	fd, err = os.OpenFile(blockPath, os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	defer fd.Close()

	_, err = fd.Seek(blockOffset, 0)
	if err != nil {
		return err
	}

	n, err := io.CopyN(fd, zero, size-blockOffset)
	if err != nil {
		return err
	}

	if n != (size - blockOffset) {
		return fmt.Errorf("Only managed to reset %d bytes out of %d", n, size)
	}

	return nil
}
