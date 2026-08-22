package cpa

import (
	"fmt"
	"os"
)

const (
	maxLogFileSize = 10 * 1024 * 1024
	maxLogBackups  = 3
)

func prepareLogFile(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogFileSize {
		for i := maxLogBackups - 1; i >= 1; i-- {
			oldPath := fmt.Sprintf("%s.%d", path, i)
			newPath := fmt.Sprintf("%s.%d", path, i+1)
			if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	for i := maxLogBackups + 1; i <= maxLogBackups+10; i++ {
		_ = os.Remove(fmt.Sprintf("%s.%d", path, i))
	}
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
}

func cleanupLogBackups(path string) error {
	for i := maxLogBackups + 1; i <= maxLogBackups+10; i++ {
		if err := os.Remove(fmt.Sprintf("%s.%d", path, i)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
