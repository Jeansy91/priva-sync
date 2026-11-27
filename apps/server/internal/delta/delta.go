// Package delta implements the server side of the rsync-style delta-sync
// protocol (ADR-0002): emitting per-block signatures of an existing file and
// reconstructing a new full file from a client-supplied plan. The server never
// stores blocks — files stay as plaintext mirrors; deltas only shrink the wire
// transfer.
package delta

import (
	"fmt"
	"io"

	"lukechampine.com/blake3"
)

// BlockSize is the fixed block size of the delta protocol (1 MiB, ADR-0002).
const BlockSize int64 = 1 << 20

// MinDeltaSize is the threshold below which a full upload is cheaper than a
// delta round-trip: delta only kicks in when the existing server file is
// strictly larger (ADR-0002).
const MinDeltaSize int64 = 1 << 20

// Signature describes one fixed-size block of a file. Weak is a fast rolling
// checksum the client uses to slide byte-by-byte; Strong is the BLAKE3 hash
// confirming a candidate match.
type Signature struct {
	Index  int    `json:"index"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Weak   uint32 `json:"weak"`
	Strong []byte `json:"strong"`
}

// BlockSignatures reads r in blockSize-byte windows and returns one Signature
// per block (the final block may be shorter).
func BlockSignatures(r io.Reader, blockSize int64) ([]Signature, error) {
	w := NewSignatureWriter(blockSize)
	if _, err := io.CopyBuffer(w, r, make([]byte, blockSize)); err != nil {
		return nil, err
	}
	return w.Signatures(), nil
}

// SignatureWriter produces the same signatures as BlockSignatures, but from a
// stream of arbitrary writes instead of a finished file. Completing an upload
// used to read the assembled bytes a second time just to fill the delta cache;
// teeing them through this writer folds both into one pass. Writes need
// not align to block boundaries — chunk sizes are the client's choice.
type SignatureWriter struct {
	blockSize int64
	buf       []byte
	sigs      []Signature
	offset    int64
	idx       int
}

func NewSignatureWriter(blockSize int64) *SignatureWriter {
	return &SignatureWriter{blockSize: blockSize, buf: make([]byte, 0, blockSize)}
}

// Write never fails: it only buffers and hashes.
func (w *SignatureWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		take := min(int(w.blockSize)-len(w.buf), len(p))
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		if int64(len(w.buf)) == w.blockSize {
			w.seal()
		}
	}
	return n, nil
}

// Signatures seals the trailing partial block and returns the signatures in
// block order. Safe to call more than once; further writes would start a new
// block, so callers must be done writing.
func (w *SignatureWriter) Signatures() []Signature {
	w.seal()
	return w.sigs
}

// seal turns the buffered block into a signature. An empty buffer produces
// nothing — a file that ends on a block boundary has no trailing block, and a
// zero-byte file has no blocks at all.
func (w *SignatureWriter) seal() {
	if len(w.buf) == 0 {
		return
	}
	strong := blake3.Sum256(w.buf)
	w.sigs = append(w.sigs, Signature{
		Index:  w.idx,
		Offset: w.offset,
		Size:   int64(len(w.buf)),
		Weak:   WeakChecksum(w.buf),
		Strong: append([]byte(nil), strong[:]...),
	})
	w.offset += int64(len(w.buf))
	w.idx++
	w.buf = w.buf[:0]
}

// weakModulus is the rsync rolling-checksum modulus (2^16). The client slides
// this checksum byte-by-byte; the server only computes it per whole block.
const weakModulus = 1 << 16

// WeakChecksum computes the classic rsync rolling checksum over a block:
//
//	a = (Σ X[i])           mod M
//	b = (Σ (n-i) X[i])     mod M
//	s = a + M*b
//
// with M = 2^16. It is fast and reproducible so the client can match blocks at
// arbitrary offsets before confirming with the strong BLAKE3 hash.
func WeakChecksum(block []byte) uint32 {
	var a, b uint32
	n := len(block)
	for i, c := range block {
		a += uint32(c)
		b += uint32(n-i) * uint32(c) //#nosec G115 -- n-i is a positive index bounded by the block length (MB-scale), far below uint32 range
	}
	a %= weakModulus
	b %= weakModulus
	return a + weakModulus*b
}

// Op is one reconstruction instruction: either Copy (reuse a byte range from the
// existing server file) or Data (literal bytes the client sent because they
// changed). A reconstruction plan is an ordered slice of Ops.
type Op interface{ isOp() }

// Copy reuses Length bytes starting at SrcOffset of the existing server file.
type Copy struct {
	SrcOffset int64
	Length    int64
}

// Data writes literal Bytes the client transmitted for a changed block.
type Data struct {
	Bytes []byte
}

func (Copy) isOp() {}
func (Data) isOp() {}

// Reconstruct applies ops in order against the existing server file (old),
// streaming the rebuilt file into w while computing its BLAKE3 hash. It returns
// the number of bytes written and the hash so the caller can verify against the
// client-declared content hash before committing. A Copy that runs past the end
// of old is an error — the whole delta fails rather than producing a truncated
// file (ADR-0002: all-or-nothing reconstruction).
func Reconstruct(old io.ReaderAt, ops []Op, w io.Writer) (written int64, hash []byte, err error) {
	hasher := blake3.New(32, nil)
	sink := io.MultiWriter(w, hasher)
	for i, op := range ops {
		switch o := op.(type) {
		case Copy:
			if o.Length < 0 || o.SrcOffset < 0 {
				return 0, nil, fmt.Errorf("op %d: negative copy range", i)
			}
			n, copyErr := io.Copy(sink, io.NewSectionReader(old, o.SrcOffset, o.Length))
			if copyErr != nil {
				return 0, nil, fmt.Errorf("op %d: copy: %w", i, copyErr)
			}
			if n != o.Length {
				return 0, nil, fmt.Errorf("op %d: copy short read: got %d want %d", i, n, o.Length)
			}
			written += n
		case Data:
			n, writeErr := sink.Write(o.Bytes)
			if writeErr != nil {
				return 0, nil, fmt.Errorf("op %d: data write: %w", i, writeErr)
			}
			written += int64(n)
		default:
			return 0, nil, fmt.Errorf("op %d: unknown op type %T", i, op)
		}
	}
	return written, hasher.Sum(nil), nil
}
