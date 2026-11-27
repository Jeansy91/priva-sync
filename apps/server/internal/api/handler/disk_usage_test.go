package handler

import "testing"

// RealDiskUsage has a platform-specific implementation (windows vs. unix)
// wired into the router via statsH.DiskUsage; exercise whichever one this
// test binary was built with directly against a real, existing directory.
func TestRealDiskUsage_ReturnsPositiveFreeAndTotal(t *testing.T) {
	free, total, err := RealDiskUsage(t.TempDir())
	if err != nil {
		t.Fatalf("RealDiskUsage: %v", err)
	}
	if total <= 0 {
		t.Errorf("total = %d, want > 0", total)
	}
	if free < 0 {
		t.Errorf("free = %d, want >= 0", free)
	}
	if free > total {
		t.Errorf("free (%d) > total (%d)", free, total)
	}
}
