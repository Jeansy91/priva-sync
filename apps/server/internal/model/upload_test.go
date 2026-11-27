package model_test

import (
	"reflect"
	"testing"

	"privasync/server/internal/model"
)

// A session with nothing received yet is missing the whole file.
func TestMissingRanges_NothingReceived_WholeFileMissing(t *testing.T) {
	got := model.MissingRanges(100, nil)
	want := []model.ByteRange{{Start: 0, End: 100}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingRanges = %v, want %v", got, want)
	}
}

// A gap in the middle — the two received edges must not appear as missing,
// and the interior gap must appear as exactly one range.
func TestMissingRanges_GapInTheMiddle(t *testing.T) {
	received := []model.ByteRange{{Start: 0, End: 10}, {Start: 40, End: 100}}
	got := model.MissingRanges(100, received)
	want := []model.ByteRange{{Start: 10, End: 40}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingRanges = %v, want %v", got, want)
	}
}

// Full coverage means nothing is missing — an empty slice, not nil vs empty
// ambiguity mattering to the caller (the handler serialises this as a JSON
// array either way), so this only asserts length.
func TestMissingRanges_EverythingReceived_NothingMissing(t *testing.T) {
	got := model.MissingRanges(100, []model.ByteRange{{Start: 0, End: 100}})
	if len(got) != 0 {
		t.Fatalf("MissingRanges = %v, want none", got)
	}
}

// Overlapping and out-of-order received ranges must not confuse the diff —
// a retried chunk can arrive again before the gap it left is detected.
func TestMissingRanges_OverlappingAndUnorderedReceived(t *testing.T) {
	received := []model.ByteRange{{Start: 50, End: 100}, {Start: 0, End: 20}, {Start: 15, End: 55}}
	got := model.MissingRanges(100, received)
	if len(got) != 0 {
		t.Fatalf("MissingRanges = %v, want none — ranges together cover [0,100)", got)
	}
}

// Adjacent received ranges (one ends exactly where the next starts) leave no
// gap between them — [0,10) then [10,20) covers [0,20) with nothing missing.
func TestMissingRanges_AdjacentReceivedRangesLeaveNoGap(t *testing.T) {
	received := []model.ByteRange{{Start: 0, End: 10}, {Start: 10, End: 20}}
	got := model.MissingRanges(20, received)
	if len(got) != 0 {
		t.Fatalf("MissingRanges = %v, want none — adjacent ranges are contiguous", got)
	}
}
