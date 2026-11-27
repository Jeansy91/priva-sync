package delta

import (
	"bytes"
	"reflect"
	"testing"

	"lukechampine.com/blake3"
)

// Slice 1 — tracer: a file shorter than one block yields exactly one
// signature describing the whole file, with the strong hash = BLAKE3(block).
func TestBlockSignatures_SingleBlock(t *testing.T) {
	data := []byte("hello delta world")
	sigs, err := BlockSignatures(bytes.NewReader(data), 1<<20)
	if err != nil {
		t.Fatalf("BlockSignatures: %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("want 1 signature, got %d", len(sigs))
	}
	s := sigs[0]
	if s.Index != 0 || s.Offset != 0 || s.Size != int64(len(data)) {
		t.Fatalf("unexpected sig geometry: %+v", s)
	}
	want := blake3.Sum256(data)
	if !bytes.Equal(s.Strong, want[:]) {
		t.Fatalf("strong hash mismatch: got %x want %x", s.Strong, want)
	}
}

// Slice 2 — a file spanning 1.5 blocks yields two signatures; the trailing
// block carries the remainder size, and offsets are contiguous.
func TestBlockSignatures_PartialTrailingBlock(t *testing.T) {
	const bs = 1024
	data := bytes.Repeat([]byte{0xAB}, bs+bs/2) // 1.5 blocks
	sigs, err := BlockSignatures(bytes.NewReader(data), bs)
	if err != nil {
		t.Fatalf("BlockSignatures: %v", err)
	}
	if len(sigs) != 2 {
		t.Fatalf("want 2 signatures, got %d", len(sigs))
	}
	if sigs[0].Offset != 0 || sigs[0].Size != bs {
		t.Fatalf("block 0 geometry: %+v", sigs[0])
	}
	if sigs[1].Index != 1 || sigs[1].Offset != bs || sigs[1].Size != bs/2 {
		t.Fatalf("block 1 geometry: %+v", sigs[1])
	}
	wantLast := blake3.Sum256(data[bs:])
	if !bytes.Equal(sigs[1].Strong, wantLast[:]) {
		t.Fatalf("trailing strong hash mismatch")
	}
}

// Slice 3 — the weak checksum is the classic rsync rolling checksum
// (a = sum bytes, b = sum of weighted bytes, s = a + M*b, M = 2^16). Pinning a
// known value locks the wire contract so the client can reproduce it.
func TestWeakChecksum_KnownValueAndDistinct(t *testing.T) {
	// "abc" = 97,98,99 -> a=294, b=3*97+2*98+1*99=586, s=294+65536*586=38404390
	if got := WeakChecksum([]byte("abc")); got != 38404390 {
		t.Fatalf("WeakChecksum(abc) = %d, want 38404390", got)
	}
	if WeakChecksum([]byte("abc")) != WeakChecksum([]byte("abc")) {
		t.Fatal("checksum not deterministic")
	}
	if WeakChecksum([]byte("abc")) == WeakChecksum([]byte("abd")) {
		t.Fatal("distinct content must yield distinct checksum")
	}
}

// Slice 4 — a plan that copies the whole server file reproduces it byte-for-byte
// and returns its BLAKE3 hash.
func TestReconstruct_AllCopyReproducesOriginal(t *testing.T) {
	old := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	var out bytes.Buffer
	written, hash, err := Reconstruct(bytes.NewReader(old), []Op{
		Copy{SrcOffset: 0, Length: int64(len(old))},
	}, &out)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if written != int64(len(old)) || !bytes.Equal(out.Bytes(), old) {
		t.Fatalf("reconstructed bytes differ (written=%d)", written)
	}
	want := blake3.Sum256(old)
	if !bytes.Equal(hash, want[:]) {
		t.Fatalf("hash mismatch: got %x want %x", hash, want)
	}
}

// Slice 5 — a plan interleaving Copy and Data ops splices literal bytes between
// reused ranges, reproducing an edited file.
func TestReconstruct_DataOpSplicesLiteralBytes(t *testing.T) {
	old := []byte("AAAABBBBCCCC")
	var out bytes.Buffer
	_, _, err := Reconstruct(bytes.NewReader(old), []Op{
		Copy{SrcOffset: 0, Length: 4},   // "AAAA"
		Data{Bytes: []byte("xx")},       // inserted
		Copy{SrcOffset: 8, Length: 4},   // "CCCC" (skips old "BBBB")
	}, &out)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if got, want := out.String(), "AAAAxxCCCC"; got != want {
		t.Fatalf("spliced output = %q, want %q", got, want)
	}
}

// Slice 6 — a Copy whose range extends past the end of the server file fails
// the whole reconstruction (all-or-nothing) instead of writing a truncated file.
func TestReconstruct_CopyOutOfBoundsFails(t *testing.T) {
	old := []byte("AAAABBBBCCCC") // 12 bytes
	var out bytes.Buffer
	_, _, err := Reconstruct(bytes.NewReader(old), []Op{
		Copy{SrcOffset: 8, Length: 100}, // wants 100, only 4 left
	}, &out)
	if err == nil {
		t.Fatal("expected out-of-bounds copy to error")
	}
}

// Copy/Data seal the Op interface via an unexported marker method that
// production code never actually invokes (Reconstruct type-switches on the
// concrete type, which doesn't call it either) — call it directly so the
// sealed-interface pattern itself has coverage.
func TestOp_MarkerMethodsAreCallable(t *testing.T) {
	Copy{}.isOp()
	Data{}.isOp()
}

// SignatureWriter must be interchangeable with BlockSignatures: the upload
// path streams its bytes through it once instead of rereading the finished
// file, so any divergence would silently poison the delta cache.
func TestSignatureWriter_MatchesBlockSignatures(t *testing.T) {
	const bs = 1024
	cases := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"below one block", bs / 3},
		{"exact block multiple", bs * 3},
		{"trailing remainder", bs*2 + 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte(i * 7)
			}
			want, err := BlockSignatures(bytes.NewReader(data), bs)
			if err != nil {
				t.Fatalf("BlockSignatures: %v", err)
			}

			sw := NewSignatureWriter(bs)
			// Write in ragged pieces: the assembler feeds whatever chunk
			// boundaries the client chose, never neat block-sized writes.
			for off := 0; off < len(data); off += 300 {
				end := min(off+300, len(data))
				if _, err := sw.Write(data[off:end]); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			got := sw.Signatures()

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("signatures differ:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}
