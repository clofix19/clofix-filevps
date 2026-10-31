package execution

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/clofix/filevps/worker/pkg/proto"
)

// ExtractProject forbids traversal, links, duplicate paths and expansion bombs.
func ExtractProject(root string, e proto.Execution) error {
	if e.Format != "zip" {
		entry := e.Entry
		if entry == "" {
			entry = "main"
		}
		if !proto.SafeExecutionPath(entry) {
			return fmt.Errorf("invalid entrypoint")
		}
		target := filepath.Join(root, filepath.FromSlash(entry))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		return os.WriteFile(target, e.Artifact, 0555)
	}
	archive, err := zip.NewReader(bytes.NewReader(e.Artifact), int64(len(e.Artifact)))
	if err != nil {
		return fmt.Errorf("invalid ZIP: %w", err)
	}
	if len(archive.File) > 4096 {
		return fmt.Errorf("ZIP contains too many files")
	}
	var total int64
	seen := map[string]bool{}
	for _, f := range archive.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !proto.SafeExecutionPath(name) || strings.HasPrefix(name, ".clofix") || f.Mode()&os.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
			return fmt.Errorf("unsafe ZIP entry: %s", f.Name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate ZIP entry: %s", name)
		}
		seen[name] = true
		dest := filepath.Join(root, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if err = os.MkdirAll(dest, 0755); err != nil {
				return err
			}
			continue
		}
		if f.UncompressedSize64 > 32<<20 {
			return fmt.Errorf("ZIP entry exceeds expansion limit")
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		source, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(source, (32<<20)-total+1))
		source.Close()
		total += int64(len(data))
		if err != nil {
			return err
		}
		if total > 32<<20 {
			return fmt.Errorf("ZIP expands beyond 32 MiB")
		}
		if err = os.WriteFile(dest, data, 0555); err != nil {
			return err
		}
	}
	entry := e.Entry
	if entry == "" {
		entry = "main"
	}
	info, err := os.Stat(filepath.Join(root, entry))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("entrypoint is missing from ZIP")
	}
	return nil
}
func CollectOutput(root string) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	count := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("output must contain regular files only")
		}
		count++
		total += info.Size()
		if count > 128 || total > 2<<20 {
			return fmt.Errorf("output exceeds 128 files or 2 MiB")
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dst, err := writer.Create(filepath.ToSlash(name))
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(dst, io.LimitReader(src, info.Size()))
		return err
	})
	if err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	return buffer.Bytes(), nil
}

// Resolve runtime executable links against the guest root, not the host /etc alternatives.
func RuntimeReady(root, runtime string) bool {
	relative := proto.ExecutionExecutable(runtime)
	if relative == "" {
		return false
	}
	for i := 0; i < 40; i++ {
		if !proto.SafeExecutionPath(filepath.ToSlash(relative)) {
			return false
		}
		file := filepath.Join(root, relative)
		info, err := os.Lstat(file)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return info.Mode().IsRegular() && info.Mode()&0111 != 0
		}
		target, err := os.Readlink(file)
		if err != nil {
			return false
		}
		if filepath.IsAbs(target) {
			relative = strings.TrimPrefix(target, "/")
		} else {
			relative = filepath.Join(filepath.Dir(relative), target)
		}
	}
	return false
}

func ExecutionDirectory() string {
	base := os.Getenv("CLOFIX_STATE_DIR")
	if base == "" {
		base = "/var/lib/clofix-filevps"
	}
	return filepath.Join(base, "executions")
}
func RuntimeDirectory() string {
	root := os.Getenv("CLOFIX_RUNTIME_DIR")
	if root != "" {
		return root
	}
	base := os.Getenv("CLOFIX_STATE_DIR")
	if base == "" {
		base = "/var/lib/clofix-filevps"
	}
	return filepath.Join(base, "runtimes")
}
