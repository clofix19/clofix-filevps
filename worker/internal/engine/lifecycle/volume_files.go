package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/clofix/filevps/worker/pkg/proto"
)

const maxVolumeFileBytes = 4 << 20

func volumePathParts(name string, allowRoot bool) ([]string, error) {
	if name == "" && allowRoot {
		return nil, nil
	}
	if name == "" || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || len(name) > 1024 {
		return nil, errors.New("invalid volume-relative path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("invalid volume-relative path")
		}
	}
	return parts, nil
}

func openVolumeDir(rootFD int, parts []string) (int, error) {
	fd, err := syscall.Dup(rootFD)
	if err != nil {
		return -1, err
	}
	for _, part := range parts {
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		_ = syscall.Close(fd)
		if err != nil {
			return -1, fmt.Errorf("open volume folder %q: %w", part, err)
		}
		fd = next
	}
	return fd, nil
}

func openVolumeParent(rootFD int, parts []string) (int, string, error) {
	if len(parts) == 0 {
		return -1, "", errors.New("volume root cannot be used for this operation")
	}
	fd, err := openVolumeDir(rootFD, parts[:len(parts)-1])
	return fd, parts[len(parts)-1], err
}

// ManageVolumeFile confines every path component with openat and O_NOFOLLOW,
// so a guest-created symlink cannot redirect admin file operations outside the volume.
func (e *Engine) ManageVolumeFile(req proto.VolumeFileRequest) (proto.VolumeFileResult, error) {
	root, err := e.volumePath(req.VolumeID)
	if err != nil {
		return proto.VolumeFileResult{}, err
	}
	if err := e.ensureVolumeMounted(req.VolumeID); err != nil {
		return proto.VolumeFileResult{}, err
	}
	parts, err := volumePathParts(req.Path, req.Action == "list")
	if err != nil {
		return proto.VolumeFileResult{}, err
	}
	rootFD, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return proto.VolumeFileResult{}, fmt.Errorf("open volume: %w", err)
	}
	defer syscall.Close(rootFD)

	switch req.Action {
	case "list":
		fd, err := openVolumeDir(rootFD, parts)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		// Use the pinned directory descriptor as the parent path. A relative name
		// here makes DirEntry.Info stat relative to the worker's cwd, hiding every
		// entry; the procfs fd path also keeps metadata lookups anchored to this
		// already-open directory.
		dir := os.NewFile(uintptr(fd), fmt.Sprintf("/proc/self/fd/%d", fd))
		defer dir.Close()
		entries, err := dir.ReadDir(-1)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		if len(entries) > 2000 {
			return proto.VolumeFileResult{}, errors.New("folder contains over 2,000 items; reduce the folder size before browsing")
		}
		out := proto.VolumeFileResult{Entries: make([]proto.VolumeFileEntry, 0, len(entries))}
		for _, item := range entries {
			info, err := item.Info()
			if err != nil {
				continue
			}
			kind := "file"
			if item.Type()&os.ModeSymlink != 0 {
				kind = "symlink"
			} else if item.IsDir() {
				kind = "folder"
			}
			childParts := append(append([]string(nil), parts...), item.Name())
			out.Entries = append(out.Entries, proto.VolumeFileEntry{Name: item.Name(), Path: strings.Join(childParts, "/"), Kind: kind, Size: info.Size(), Modified: info.ModTime()})
		}
		return out, nil
	case "read":
		parentFD, name, err := openVolumeParent(rootFD, parts)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		defer syscall.Close(parentFD)
		fd, err := syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return proto.VolumeFileResult{}, fmt.Errorf("open volume file: %w", err)
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		if !st.Mode().IsRegular() {
			return proto.VolumeFileResult{}, errors.New("only regular files can be downloaded")
		}
		if st.Size() > maxVolumeFileBytes {
			return proto.VolumeFileResult{}, errors.New("file exceeds the 4 MB explorer limit")
		}
		content, err := io.ReadAll(io.LimitReader(f, maxVolumeFileBytes+1))
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		if len(content) > maxVolumeFileBytes {
			return proto.VolumeFileResult{}, errors.New("file exceeds the 4 MB explorer limit")
		}
		return proto.VolumeFileResult{Content: content}, nil
	case "write":
		if len(req.Content) > maxVolumeFileBytes {
			return proto.VolumeFileResult{}, errors.New("upload exceeds the 4 MB explorer limit")
		}
		parentFD, name, err := openVolumeParent(rootFD, parts)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		defer syscall.Close(parentFD)
		fd, err := syscall.Openat(parentFD, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o644)
		if err != nil {
			return proto.VolumeFileResult{}, fmt.Errorf("write volume file: %w", err)
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		if _, err := f.Write(req.Content); err != nil {
			return proto.VolumeFileResult{}, err
		}
		return proto.VolumeFileResult{}, f.Sync()
	case "mkdir":
		parentFD, name, err := openVolumeParent(rootFD, parts)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		defer syscall.Close(parentFD)
		if err := syscall.Mkdirat(parentFD, name, 0o755); err != nil {
			if !errors.Is(err, syscall.EEXIST) {
				return proto.VolumeFileResult{}, fmt.Errorf("create volume folder: %w", err)
			}
			fd, openErr := syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
			if openErr != nil {
				return proto.VolumeFileResult{}, fmt.Errorf("volume path already exists and is not a safe directory: %w", openErr)
			}
			_ = syscall.Close(fd)
		}
		return proto.VolumeFileResult{}, nil
	case "delete":
		parentFD, name, err := openVolumeParent(rootFD, parts)
		if err != nil {
			return proto.VolumeFileResult{}, err
		}
		defer syscall.Close(parentFD)
		if err := removeVolumeEntry(parentFD, name, req.Recursive); err != nil {
			return proto.VolumeFileResult{}, err
		}
		return proto.VolumeFileResult{}, nil
	default:
		return proto.VolumeFileResult{}, errors.New("unsupported volume file action")
	}
}

func removeVolumeEntry(parentFD int, name string, recursive bool) error {
	fd, err := syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err == nil {
		dir := os.NewFile(uintptr(fd), name)
		children, readErr := dir.ReadDir(-1)
		if readErr != nil {
			dir.Close()
			return readErr
		}
		if len(children) > 0 && !recursive {
			dir.Close()
			return errors.New("folder is not empty; enable recursive delete to remove its contents")
		}
		for _, child := range children {
			if err := removeVolumeEntry(fd, child.Name(), true); err != nil {
				dir.Close()
				return err
			}
		}
		dir.Close()
		if err := os.Remove(fmt.Sprintf("/proc/self/fd/%d/%s", parentFD, name)); err != nil {
			return fmt.Errorf("remove volume folder: %w", err)
		}
		return nil
	}
	if !errors.Is(err, syscall.ENOTDIR) && !errors.Is(err, syscall.ELOOP) {
		return err
	}
	if err := syscall.Unlinkat(parentFD, name); err != nil {
		return fmt.Errorf("remove volume file: %w", err)
	}
	return nil
}
