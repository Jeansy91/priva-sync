//go:build windows

package handler

import "golang.org/x/sys/windows"

// RealDiskUsage returns free + total bytes for the volume hosting dir.
func RealDiskUsage(dir string) (free, total int64, err error) {
	dirPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var freeBytesAvail, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(dirPtr, &freeBytesAvail, &totalBytes, &totalFreeBytes); err != nil {
		return 0, 0, err
	}
	return int64(freeBytesAvail), int64(totalBytes), nil
}
