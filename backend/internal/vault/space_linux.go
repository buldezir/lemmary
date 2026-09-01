//go:build linux

package vault

import "syscall"

// availableBytes reports the free space usable by an unprivileged process.
func availableBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Filesystem magic numbers for the memory-backed filesystems.
const (
	tmpfsMagic = 0x01021994
	ramfsMagic = 0x858458f6
)

// isMemoryBacked reports whether path lives on tmpfs or ramfs.
func isMemoryBacked(path string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false, err
	}
	t := int64(st.Type)
	return t == tmpfsMagic || t == ramfsMagic, nil
}
