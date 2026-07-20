package api

import (
	"os"
	"path/filepath"
)

// dataDir lets multiple node processes run on one machine with
// isolated blockchain/state/identity files, by pointing each at its
// own directory via DATA_DIR. Defaults to the working directory so a
// single-node run behaves exactly as before.
func dataDir() string {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		return "."
	}
	return dir
}

func dataPath(name string) string {
	return filepath.Join(dataDir(), name)
}
