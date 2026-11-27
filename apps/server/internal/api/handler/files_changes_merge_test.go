package handler

import (
	"testing"

	"privasync/server/internal/model"
)

func fileWithSeq(seq int64) model.File { return model.File{Seq: seq} }
func moveWithSeq(seq int64) model.MoveEvent { return model.MoveEvent{Seq: seq} }

// The core ADR-0065 guarantee: a move_events row is a first-class feed entry,
// ordered against files rows purely by seq, regardless of which source it
// came from.
func TestMergeChangeFeed_InterleavesBothSourcesBySeq(t *testing.T) {
	files := []model.File{fileWithSeq(1), fileWithSeq(3), fileWithSeq(5)}
	moves := []model.MoveEvent{moveWithSeq(2), moveWithSeq(4)}

	outFiles, outMoves, cursor, hasMore := mergeChangeFeed(files, moves, 10)

	if len(outFiles) != 3 || len(outMoves) != 2 {
		t.Fatalf("outFiles=%d outMoves=%d, want all consumed (5 total < limit 10)", len(outFiles), len(outMoves))
	}
	if cursor != 5 {
		t.Fatalf("cursor = %d, want 5 (the highest seq across both sources)", cursor)
	}
	if hasMore {
		t.Fatalf("hasMore = true, want false -- both sources fully drained")
	}
}

func TestMergeChangeFeed_TruncatesAtLimitAndSignalsMore(t *testing.T) {
	// limit+1 overfetch per source (as GetChanges will call it): 3 files + 1
	// move requested with limit=3, so the caller passed limit+1=4 per query.
	files := []model.File{fileWithSeq(1), fileWithSeq(2), fileWithSeq(4)}
	moves := []model.MoveEvent{moveWithSeq(3)}

	outFiles, outMoves, cursor, hasMore := mergeChangeFeed(files, moves, 3)

	if len(outFiles) != 2 || len(outMoves) != 1 {
		t.Fatalf("outFiles=%d outMoves=%d, want 2 files + 1 move for the first 3 by seq (1,2,3)", len(outFiles), len(outMoves))
	}
	if cursor != 3 {
		t.Fatalf("cursor = %d, want 3 (seq of the 3rd item, the move)", cursor)
	}
	if !hasMore {
		t.Fatalf("hasMore = false, want true -- seq=4 file was truncated off")
	}
}

// A source that fills its own limit+1 window must signal more even when the
// combined truncation happens to consume all of it (the other source is
// empty) -- this is exactly what the limit+1 overfetch exists to catch.
func TestMergeChangeFeed_SourceFillingItsOwnWindowSignalsMoreEvenIfFullyConsumed(t *testing.T) {
	// limit=3, files queried with LIMIT 4 and got exactly 4 back -- there
	// could be a 5th row past this window even though only 3 are emitted.
	files := []model.File{fileWithSeq(1), fileWithSeq(2), fileWithSeq(3), fileWithSeq(4)}

	outFiles, outMoves, cursor, hasMore := mergeChangeFeed(files, nil, 3)

	if len(outFiles) != 3 || len(outMoves) != 0 {
		t.Fatalf("outFiles=%d outMoves=%d, want 3 files, no moves", len(outFiles), len(outMoves))
	}
	if cursor != 3 {
		t.Fatalf("cursor = %d, want 3", cursor)
	}
	if !hasMore {
		t.Fatalf("hasMore = false, want true -- the 4th file row proves more is waiting")
	}
}

func TestMergeChangeFeed_EmptyInputsReportNoMore(t *testing.T) {
	outFiles, outMoves, cursor, hasMore := mergeChangeFeed(nil, nil, 10)
	if len(outFiles) != 0 || len(outMoves) != 0 || cursor != 0 || hasMore {
		t.Fatalf("merge of empty inputs = files=%v moves=%v cursor=%d hasMore=%v, want all zero/empty",
			outFiles, outMoves, cursor, hasMore)
	}
}
