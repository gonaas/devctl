package gitx

import (
	"os"
	"path/filepath"
)

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func writeFile(directory, name, content string) error {
	return os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644)
}
