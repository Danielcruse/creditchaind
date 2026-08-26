package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (bc *Blockchain) Save(
	filename string,
) error {
	data, err := json.MarshalIndent(
		bc,
		"",
		"  ",
	)

	if err != nil {
		return err
	}

	return AtomicWriteFile(filename, data, 0644)
}

// AtomicWriteFile writes via a temp file in the same directory + rename,
// instead of truncating filename in place - an in-place truncate leaves
// a window where a reader (a backup's tar, a crash mid-write) sees a
// short or zero-length file. Same directory matters: os.Rename is only
// atomic within a single filesystem/volume.
func AtomicWriteFile(filename string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(
		filepath.Dir(filename),
		filepath.Base(filename)+".tmp-*",
	)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filename); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func LoadBlockchain(
	filename string,
) (*Blockchain, error) {

	data, err := os.ReadFile(
		filename,
	)

	if err != nil {
		return nil, err
	}

	var bc Blockchain

	err = json.Unmarshal(
		data,
		&bc,
	)

	if err != nil {
		return nil, err
	}

	return &bc, nil
}