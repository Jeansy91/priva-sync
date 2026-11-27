package repository

// pathPrefixRange turns a path prefix into the half-open byte range holding
// exactly the paths that begin with it, so a subtree can be selected with two
// ordinary comparisons instead of a LIKE pattern.
//
// A pattern is the wrong tool here twice over. A stored path carries no
// escaping, so "%" and "_" inside a directory name silently widen the match --
// "/a_b" reaches into "/axb". And Postgres cannot derive index
// range bounds from a pattern that arrives as a parameter, so the query scans
// the whole root no matter which index exists. Bounds have neither
// problem, and there is no escaping left to forget at the next call site.
//
// ok is false when the prefix has no successor: it is empty, or made only of
// 0xFF bytes. Callers pass a prefix ending in "/", which always has one.
//
// The comparison must be byte-wise (~>=~ / ~<~ over text_pattern_ops), not
// collation-aware: under en_US.utf8 "sorts between X and X's successor" is a
// different set from "starts with X".
func pathPrefixRange(prefix string) (lo, hi string, ok bool) {
	for i := len(prefix) - 1; i >= 0; i-- {
		if prefix[i] != 0xFF {
			return prefix, prefix[:i] + string([]byte{prefix[i] + 1}), true
		}
	}
	return prefix, "", false
}

// subtreeRange is the pair every subtree query compares against: the
// directory's own path, and the byte range its descendants occupy.
//
// Callers spell the root either as "" or as "/" -- a share's path is "/" for a
// root-share. Both have to mean the whole tree, and neither may produce the
// doubled separator that "/" + "/" used to yield, which matched nothing at all
//.
func subtreeRange(dirPath string) (self, lo, hi string) {
	self = dirPath
	for len(self) > 0 && self[len(self)-1] == '/' {
		self = self[:len(self)-1]
	}
	lo, hi, _ = pathPrefixRange(self + "/")
	return self, lo, hi
}
