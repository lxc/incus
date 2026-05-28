package local

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/lxc/incus/v7/shared/logger"
)

// metaSuffix is appended to the data filename to form the metadata.
const metaSuffix = ".meta"

// objectMeta is the metadata stored alongside object data files.
type objectMeta struct {
	ContentType string            `json:"content_type,omitempty"`
	ETag        string            `json:"etag"`
	Size        int64             `json:"size"`
	LastMod     time.Time         `json:"last_modified"`
	UserMeta    map[string]string `json:"user_meta,omitempty"`
}

func readMeta(metaPath string) (*objectMeta, error) {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}

	m := &objectMeta{}
	err = json.Unmarshal(b, m)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func writeMeta(metaPath string, m *objectMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}

	tmp := metaPath + ".tmp"
	err = os.WriteFile(tmp, b, 0o600)
	if err != nil {
		return err
	}

	return os.Rename(tmp, metaPath)
}

func metaPathFor(dataPath string) string {
	return dataPath + metaSuffix
}

func removeMeta(metaPath string) error {
	err := os.Remove(metaPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}

func loadOrInferMeta(dataPath string) (*objectMeta, error) {
	meta, err := readMeta(metaPathFor(dataPath))
	if err == nil {
		return meta, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	st, err := os.Stat(dataPath)
	if err != nil {
		return nil, err
	}

	if st.IsDir() {
		return nil, fs.ErrNotExist
	}

	f, err := os.Open(dataPath)
	if err != nil {
		return nil, err
	}

	defer logger.WarnOnError(f.Close, "Failed to close file")

	hasher := md5.New()
	_, err = io.Copy(hasher, f)
	if err != nil {
		return nil, err
	}

	meta = &objectMeta{
		ETag:    hex.EncodeToString(hasher.Sum(nil)),
		Size:    st.Size(),
		LastMod: st.ModTime().UTC(),
	}

	_ = writeMeta(metaPathFor(dataPath), meta)

	return meta, nil
}
