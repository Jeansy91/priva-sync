package handler

import "testing"

// The escape guard used to be buried inside the mount handlers (join +
// isUnderShareRoot re-inlined per mutation). ADR-0017 lifts it into a pure
// helper so the security boundary is testable without going through HTTP.
func TestJoinUnderShareRoot(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		sub      string
		wantPath string
		wantOK   bool
	}{
		{"in bounds", "/share", "a/b.txt", "/share/a/b.txt", true},
		{"leading slash on sub", "/share", "/a/b.txt", "/share/a/b.txt", true},
		{"exact root", "/share", "", "/share", true},
		{"parent escape", "/share", "../secret", "", false},
		{"sibling prefix escape", "/share", "../share-evil/x", "", false},
		{"own root has no prefix", "", "foo.txt", "/foo.txt", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := joinUnderShareRoot(tc.base, tc.sub)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.wantPath {
				t.Fatalf("path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}
