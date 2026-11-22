package main

import (
	"os"
	"path/filepath"
)

// replacePrivateFile publishes data atomically through a 0600 temporary file.
func replacePrivateFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".kash-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
