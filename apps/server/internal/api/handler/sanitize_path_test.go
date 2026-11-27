package handler

import "testing"

// path.Clean on an absolute path fully resolves and discards any ".." without
// ever escaping root, so sanitizePath never actually errors — the error
// return stays for its ~14 call sites, which all branch on it. This test
// exists to lock in that guarantee, especially the false-positive that used
// to hit legitimate filenames containing ".." as a substring (e.g. ROM dumps
// like "Super Mario Bros..nes"), which previously failed uploads with 400
// forever.
func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain path", "/a/b.txt", "/a/b.txt"},
		{"double dot inside filename", "/Wii/SD Card/fceugx/roms/Super Mario Bros..nes", "/Wii/SD Card/fceugx/roms/Super Mario Bros..nes"},
		{"double dot as directory name", "/my..folder/file.txt", "/my..folder/file.txt"},
		{"relative traversal collapses safely", "/a/../b", "/b"},
		{"traversal above root discarded", "/../../etc/passwd", "/etc/passwd"},
		{"bare traversal collapses to root", "/..", "/"},
		{"traversal segment mid path collapses to root", "/a/../../..", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizePath(tt.in)
			if err != nil {
				t.Fatalf("sanitizePath(%q) returned unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("sanitizePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
