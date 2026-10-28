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
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("CPA 日志必须为普通文件")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for i := 0; i <= maxLogBackups; i++ {
		logPath := path
		if i > 0 {
			logPath = fmt.Sprintf("%s.%d", path, i)
		}
		if info, err := os.Lstat(logPath); err == nil {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("CPA 日志必须为普通文件")
			}
			if err := os.Chmod(logPath, 0600); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func cleanupLogBackups(path string) error {
	for i := maxLogBackups + 1; i <= maxLogBackups+10; i++ {
		if err := os.Remove(fmt.Sprintf("%s.%d", path, i)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
