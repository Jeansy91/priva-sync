//go:build !windows

package handler

import "syscall"

// RealDiskUsage returns free + total bytes for the filesystem hosting dir.
func RealDiskUsage(dir string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	//#nosec G115 -- Statfs block counts/sizes for a real filesystem never approach int64 overflow
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}
