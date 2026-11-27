package handler

import "privasync/server/internal/model"

// mergeChangeFeed combines a files page and a move_events page (ADR-0065)
// into one seq-ordered result capped at limit. Both inputs must already be
// sorted by seq ascending and fetched with LIMIT limit+1 -- the one extra row
// per source is what lets hasMore be determined independently of how the
// merge happens to split between the two sources (a source that filled its
// own limit+1 window always signals more on its own, regardless of how many
// of its rows survive the combined truncation).
//
// The returned slices are prefixes of the inputs (stable order preserved),
// which is what apply_renames-style consumption on the client expects.
func mergeChangeFeed(files []model.File, moves []model.MoveEvent, limit int) (outFiles []model.File, outMoves []model.MoveEvent, nextCursor int64, hasMore bool) {
	fi, mi := 0, 0
	for taken := 0; taken < limit && (fi < len(files) || mi < len(moves)); taken++ {
		takeFile := mi >= len(moves) || (fi < len(files) && files[fi].Seq < moves[mi].Seq)
		if takeFile {
			nextCursor = files[fi].Seq
			fi++
		} else {
			nextCursor = moves[mi].Seq
			mi++
		}
	}
	hasMore = fi < len(files) || mi < len(moves)
	return files[:fi], moves[:mi], nextCursor, hasMore
}
