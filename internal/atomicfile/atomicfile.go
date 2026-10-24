// Package atomicfile replaces a regular file by writing a temporary sibling
// and renaming it into place. A symlink or other non-regular target is left
// untouched.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Write creates or replaces path with data. mode is applied before the file
// becomes visible at path.
func Write(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write into %s", dir)
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace a non-regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	keep = true
	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
