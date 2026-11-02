// Package logtail tails registered log files for the worker heartbeat.
package logtail

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MaxBytes = 64 << 10

var (
	ErrPath = errors.New("invalid log path")
	ErrHost = errors.New("worker host logs must stay under /var/log")
)

func CleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "docker" {
		return path, nil
	}
	if strings.Contains(path, "\x00") || strings.Contains(path, "..") {
		return "", ErrPath
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "..") {
		return "", ErrPath
	}
	if strings.Contains(cleaned, "..") {
		return "", ErrPath
	}
	return cleaned, nil
}

func AllowHostPath(path string) error {
	cleaned, err := CleanPath(path)
	if err != nil {
		return err
	}
	if cleaned == "" || cleaned == "docker" {
		return ErrHost
	}
	if !filepath.IsAbs(cleaned) {
		return ErrHost
	}
	rel, err := filepath.Rel("/var/log", cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ErrHost
	}
	return nil
}

func GuestPath(pid int, path string) (string, error) {
	cleaned, err := CleanPath(path)
	if err != nil || cleaned == "" || cleaned == "docker" {
		return "", ErrPath
	}
	if !filepath.IsAbs(cleaned) {
		cleaned = "/" + cleaned
	}
	root := filepath.Join("/proc", strconv.Itoa(pid), "root")
	full := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", ErrPath
	}
	return full, nil
}

func Confine(root, path string) (string, error) {
	cleaned, err := CleanPath(path)
	if err != nil {
		return "", err
	}
	if cleaned == "" || cleaned == "docker" {
		return "", nil
	}
	base := filepath.Clean(root)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(base, cleaned)
	}
	rel, err := filepath.Rel(base, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", ErrPath
	}
	return cleaned, nil
}

func TailFile(path string, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info.Size() > int64(limit) {
		_, _ = f.Seek(-int64(limit), io.SeekEnd)
	}
	b, _ := io.ReadAll(io.LimitReader(f, int64(limit)))
	return b
}
