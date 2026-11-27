package repository

import "testing"

func TestPathPrefixRange(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		wantHi string
		wantOK bool
	}{
		{name: "directory prefix", prefix: "/dir/", wantHi: "/dir0", wantOK: true},
		{name: "root prefix covers every path", prefix: "/", wantHi: "0", wantOK: true},
		{name: "underscore is data, not a wildcard", prefix: "/a_b/", wantHi: "/a_b0", wantOK: true},
		// A trailing 0xFF has no successor of its own; the bound has to come
		// from the last byte that does, with the 0xFF tail dropped.
		{name: "trailing max byte carries", prefix: "/a\xff", wantHi: "/b", wantOK: true},
		{name: "all max bytes have no upper bound", prefix: "\xff\xff", wantOK: false},
		{name: "empty prefix has no upper bound", prefix: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi, ok := pathPrefixRange(tt.prefix)
			if ok != tt.wantOK {
				t.Fatalf("pathPrefixRange(%q) ok = %v, want %v", tt.prefix, ok, tt.wantOK)
			}
			if lo != tt.prefix {
				t.Errorf("lo = %q, want the prefix itself %q", lo, tt.prefix)
			}
			if ok && hi != tt.wantHi {
				t.Errorf("hi = %q, want %q", hi, tt.wantHi)
			}
		})
	}
}

// The bound must exclude every sibling that merely shares a textual head:
// "/a_b/" must not admit "/a_b_x/..." from outside the directory, and must
// admit everything inside it.
func TestPathPrefixRange_BoundsMatchExactlyThePrefix(t *testing.T) {
	lo, hi, ok := pathPrefixRange("/a_b/")
	if !ok {
		t.Fatal("pathPrefixRange(\"/a_b/\") reported no upper bound")
	}

	inside := func(p string) bool { return p >= lo && p < hi }

	for _, p := range []string{"/a_b/x.txt", "/a_b/deep/y.txt", "/a_b/"} {
		if !inside(p) {
			t.Errorf("%q should be inside [%q, %q)", p, lo, hi)
		}
	}
	for _, p := range []string{"/a_b", "/axb/x.txt", "/a_bx/y.txt", "/b/x.txt"} {
		if inside(p) {
			t.Errorf("%q should be outside [%q, %q)", p, lo, hi)
		}
	}
}

func TestSubtreeRange_RootSpellings(t *testing.T) {
	for _, dirPath := range []string{"", "/"} {
		self, lo, hi := subtreeRange(dirPath)
		if self != "" {
			t.Errorf("subtreeRange(%q) self = %q, want the empty own-path", dirPath, self)
		}
		if lo != "/" || hi != "0" {
			t.Errorf("subtreeRange(%q) = [%q, %q), want [\"/\", \"0\")", dirPath, lo, hi)
		}
	}
}

func TestSubtreeRange_NestedDirectory(t *testing.T) {
	self, lo, hi := subtreeRange("/dir/sub")
	if self != "/dir/sub" || lo != "/dir/sub/" || hi != "/dir/sub0" {
		t.Fatalf("subtreeRange(\"/dir/sub\") = %q, [%q, %q)", self, lo, hi)
	}
}
