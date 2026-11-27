//! Client side of the rsync-style delta-sync protocol (ADR-0002).
//!
//! Block-matching: given the server's per-block signatures of the *old* file,
//! build a copy/data plan that reconstructs the *new* local file while sending
//! only the bytes that changed. Correctness never depends on match quality —
//! any region we fail to match rides along as literal `Data`, and the server
//! verifies the reconstructed BLAKE3 hash before committing.
//!
//! The matcher streams the local file through a bounded sliding buffer
//! ([`compute_plan_reader`]) so even multi-gigabyte files are diffed without
//! loading them into memory; it also hashes the file in the same pass. A literal
//! cap aborts the diff (`NotWorthwhile`) once the changed payload rivals a plain
//! upload, which both bounds the plan's memory and avoids a pointless delta.
//! [`compute_plan`] is the in-memory convenience wrapper over the same core.

/// Fixed delta block size, mirrors the server (ADR-0002: 1 MiB).
pub const BLOCK_SIZE: usize = 1 << 20;

/// One block signature of the server's current file, as returned by
/// `GET …/files/delta`. `strong` holds the 32-byte BLAKE3 digest (the wire form
/// base64-decodes into these bytes); `weak` is the rsync rolling checksum.
#[derive(Debug, Clone)]
pub struct BlockSignature {
    pub offset: i64,
    pub size:   i64,
    pub weak:   u32,
    pub strong: Vec<u8>,
}

/// One reconstruction instruction. `Copy` reuses a byte range of the server's
/// old file; `Data` carries literal bytes the client must transmit. An ordered
/// slice of these rebuilds the new file (mirrors the server's `delta.Op`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DeltaOp {
    Copy { offset: i64, length: i64 },
    Data(Vec<u8>),
}

/// rsync rolling-checksum modulus (2^16), identical to the server's.
const WEAK_MODULUS: u32 = 1 << 16;

/// Compute the rsync rolling checksum of a whole block, bit-for-bit identical to
/// the server's `WeakChecksum`:
///
/// ```text
/// a = (Σ X[i])        mod M
/// b = (Σ (n-i) X[i])  mod M
/// s = a + M*b
/// ```
///
/// The client never signs blocks itself (the server does) — this exists so the
/// tests can reproduce the server's signatures exactly.
#[cfg(test)]
pub fn weak_checksum(block: &[u8]) -> u32 {
    let n = block.len() as u32;
    let mut a: u32 = 0;
    let mut b: u32 = 0;
    for (i, &c) in block.iter().enumerate() {
        a = a.wrapping_add(c as u32);
        b = b.wrapping_add((n - i as u32).wrapping_mul(c as u32));
    }
    (a % WEAK_MODULUS) + WEAK_MODULUS * (b % WEAK_MODULUS)
}

/// Sliding rsync rolling checksum: maintained over a fixed `n`-byte window so we
/// can advance one byte at a time without rehashing the whole window. The value
/// it yields equals `weak_checksum` of the current window.
struct RollingChecksum {
    a: u32,
    b: u32,
    n: u32,
}

impl RollingChecksum {
    fn new(block: &[u8]) -> Self {
        let n = block.len() as u32;
        let mut a: u32 = 0;
        let mut b: u32 = 0;
        for (i, &c) in block.iter().enumerate() {
            a = a.wrapping_add(c as u32);
            b = b.wrapping_add((n - i as u32).wrapping_mul(c as u32));
        }
        Self { a: a % WEAK_MODULUS, b: b % WEAK_MODULUS, n }
    }

    fn value(&self) -> u32 {
        (self.a % WEAK_MODULUS) + WEAK_MODULUS * (self.b % WEAK_MODULUS)
    }

    /// Slide the window one byte right: drop `old` (leftmost), add `new` (the
    /// byte entering on the right). Derivation: see ADR-0002 / the server's
    /// WeakChecksum — `a' = a - old + new`, `b' = b - n*old + a'`.
    fn roll(&mut self, old: u8, new: u8) {
        self.a = self
            .a
            .wrapping_sub(old as u32)
            .wrapping_add(new as u32)
            % WEAK_MODULUS;
        self.b = self
            .b
            .wrapping_sub(self.n.wrapping_mul(old as u32))
            .wrapping_add(self.a)
            % WEAK_MODULUS;
    }
}

/// Append a Copy op, merging into the previous op when it copies the directly
/// preceding server range — keeps consecutive matched blocks one instruction.
fn push_copy(ops: &mut Vec<DeltaOp>, offset: i64, length: i64) {
    if let Some(DeltaOp::Copy { offset: o, length: l }) = ops.last_mut() {
        if *o + *l == offset {
            *l += length;
            return;
        }
    }
    ops.push(DeltaOp::Copy { offset, length });
}

/// Append literal bytes, merging into a trailing Data op so adjacent literals
/// stay a single instruction. Empty slices are ignored.
fn push_data(ops: &mut Vec<DeltaOp>, bytes: &[u8]) {
    if bytes.is_empty() {
        return;
    }
    if let Some(DeltaOp::Data(buf)) = ops.last_mut() {
        buf.extend_from_slice(bytes);
        return;
    }
    ops.push(DeltaOp::Data(bytes.to_vec()));
}

/// Read granularity for the streaming matcher. Small relative to a block so the
/// sliding buffer stays tight; large enough to keep syscalls modest.
const READ_CHUNK: usize = 64 * 1024;

/// Result of a streaming diff.
pub enum StreamingPlan {
    /// A usable plan plus the full-file BLAKE3 hash computed in the same pass.
    Delta {
        ops:  Vec<DeltaOp>,
        hash: blake3::Hash,
    },
    /// The changed payload exceeded `max_literal_bytes` — delta saves little, so
    /// the caller should fall back to a plain full/chunked upload.
    NotWorthwhile,
}

/// Read more bytes from `reader` into `buf`, hashing them as they arrive so the
/// full-file digest is ready without a second pass. Sets `eof` on a 0-byte read.
fn fill<R: std::io::Read>(
    reader: &mut R,
    buf:    &mut Vec<u8>,
    hasher: &mut blake3::Hasher,
    eof:    &mut bool,
) -> std::io::Result<()> {
    let mut tmp = [0u8; READ_CHUNK];
    let k = reader.read(&mut tmp)?;
    if k == 0 {
        *eof = true;
    } else {
        hasher.update(&tmp[..k]);
        buf.extend_from_slice(&tmp[..k]);
    }
    Ok(())
}

/// Stream `reader` (the new local file) against the server's block `sigs` and
/// build a reconstruction plan, sending only changed bytes as `Data` ops.
///
/// Only full `block_size` server blocks take part in matching; the server's
/// trailing partial block is intentionally skipped (design decision) — at
/// worst re-sent as literal data, never miscopied. A rolling weak checksum
/// slides byte-by-byte and every candidate is confirmed with the strong BLAKE3
/// hash before a Copy is emitted, so a Copy can never reference bytes that
/// differ from the target.
///
/// Memory is bounded: matched/copied bytes are dropped from the buffer as soon
/// as a block matches, and once the pending or total literal payload exceeds
/// `max_literal_bytes` the diff aborts with `NotWorthwhile`.
pub fn compute_plan_reader<R: std::io::Read>(
    mut reader:        R,
    sigs:              &[BlockSignature],
    block_size:        usize,
    max_literal_bytes: usize,
) -> std::io::Result<StreamingPlan> {
    // Index full-size blocks by weak checksum for O(1) candidate lookup.
    let mut by_weak: std::collections::HashMap<u32, Vec<&BlockSignature>> =
        std::collections::HashMap::new();
    for s in sigs {
        if s.size as usize == block_size {
            by_weak.entry(s.weak).or_default().push(s);
        }
    }

    let n = block_size;
    let mut ops: Vec<DeltaOp> = Vec::new();
    let mut hasher = blake3::Hasher::new();
    let mut buf: Vec<u8> = Vec::new();
    let mut eof = false;
    // `buf[0]` is always the start of the pending literal run: a match drains
    // everything up to and including the matched window, so `pos` (the window
    // start) doubles as the pending-literal length.
    let mut pos = 0usize;
    let mut literal_total = 0usize;
    let mut roll: Option<RollingChecksum> = None;

    loop {
        while buf.len() < pos + n && !eof {
            fill(&mut reader, &mut buf, &mut hasher, &mut eof)?;
        }
        if buf.len() < pos + n {
            break; // not enough bytes for a full window — the rest is tail literal
        }

        if roll.is_none() {
            roll = Some(RollingChecksum::new(&buf[pos..pos + n]));
        }
        let weak = roll.as_ref().unwrap().value();

        let matched = by_weak.get(&weak).and_then(|cands| {
            let strong = blake3::hash(&buf[pos..pos + n]);
            cands
                .iter()
                .find(|s| s.strong.as_slice() == strong.as_bytes())
                .copied()
        });

        if let Some(s) = matched {
            if pos > 0 {
                literal_total += pos;
                if literal_total > max_literal_bytes {
                    return Ok(StreamingPlan::NotWorthwhile);
                }
                push_data(&mut ops, &buf[..pos]);
            }
            push_copy(&mut ops, s.offset, s.size);
            buf.drain(..pos + n);
            pos = 0;
            roll = None;
        } else {
            // Slide one byte; rolling needs the byte entering at pos + n.
            while buf.len() < pos + n + 1 && !eof {
                fill(&mut reader, &mut buf, &mut hasher, &mut eof)?;
            }
            if buf.len() < pos + n + 1 {
                break; // EOF: the unmatched final window is tail literal
            }
            roll.as_mut().unwrap().roll(buf[pos], buf[pos + n]);
            pos += 1;
            if pos > max_literal_bytes {
                return Ok(StreamingPlan::NotWorthwhile); // pending literal too large
            }
        }
    }

    // Drain the rest of the file so the hash covers every byte, even if we broke
    // before EOF (we only break once `eof` is set, but stay explicit).
    while !eof {
        fill(&mut reader, &mut buf, &mut hasher, &mut eof)?;
    }

    if !buf.is_empty() {
        literal_total += buf.len();
        if literal_total > max_literal_bytes {
            return Ok(StreamingPlan::NotWorthwhile);
        }
        push_data(&mut ops, &buf);
    }

    Ok(StreamingPlan::Delta { ops, hash: hasher.finalize() })
}

/// In-memory convenience wrapper over [`compute_plan_reader`]: diff `target`
/// against `sigs` with no literal cap. Production always streams from a file via
/// `compute_plan_reader`; this slice form keeps the algorithm tests readable.
#[cfg(test)]
pub fn compute_plan(target: &[u8], sigs: &[BlockSignature], block_size: usize) -> Vec<DeltaOp> {
    match compute_plan_reader(std::io::Cursor::new(target), sigs, block_size, usize::MAX) {
        // The cap is unbounded and `Cursor` reads never fail, so the diff always
        // yields a plan here.
        Ok(StreamingPlan::Delta { ops, .. }) => ops,
        Ok(StreamingPlan::NotWorthwhile) => unreachable!("unbounded literal cap"),
        Err(e) => unreachable!("in-memory Cursor reads cannot fail: {e}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Apply a plan against the old server bytes, mirroring the server's
    /// Reconstruct — the test oracle that proves a plan rebuilds the target.
    fn apply_plan(old: &[u8], ops: &[DeltaOp]) -> Vec<u8> {
        let mut out = Vec::new();
        for op in ops {
            match op {
                DeltaOp::Copy { offset, length } => {
                    let s = *offset as usize;
                    let e = s + *length as usize;
                    out.extend_from_slice(&old[s..e]);
                }
                DeltaOp::Data(bytes) => out.extend_from_slice(bytes),
            }
        }
        out
    }

    /// Build signatures for `old` exactly as the server would (full-size blocks
    /// plus a possibly shorter trailing block).
    fn sign(old: &[u8], block_size: usize) -> Vec<BlockSignature> {
        let mut sigs = Vec::new();
        let mut offset = 0usize;
        while offset < old.len() {
            let end = (offset + block_size).min(old.len());
            let block = &old[offset..end];
            sigs.push(BlockSignature {
                offset: offset as i64,
                size:   block.len() as i64,
                weak:   super::weak_checksum(block),
                strong: blake3::hash(block).as_bytes().to_vec(),
            });
            offset = end;
        }
        sigs
    }

    /// Sum of literal bytes a plan would put on the wire.
    fn data_bytes(ops: &[DeltaOp]) -> usize {
        ops.iter()
            .map(|o| match o {
                DeltaOp::Data(b) => b.len(),
                DeltaOp::Copy { .. } => 0,
            })
            .sum()
    }

    // Slice 1 (tracer): a target identical to one whole server block becomes a
    // single Copy of that block, with no literal Data, and rebuilds the target.
    #[test]
    fn identical_single_block_is_one_copy() {
        let bs = 16;
        let old: Vec<u8> = (0..bs as u8).collect();
        let target = old.clone();
        let sigs = sign(&old, bs);

        let plan = compute_plan(&target, &sigs, bs);

        assert_eq!(plan, vec![DeltaOp::Copy { offset: 0, length: bs as i64 }]);
        assert_eq!(apply_plan(&old, &plan), target);
    }

    // Slice 2: with no server signatures (no base to diff against) the whole
    // target is one literal Data op — degrades cleanly to a full upload.
    #[test]
    fn no_signatures_yields_single_data_op() {
        let target: Vec<u8> = (0..40u8).collect();

        let plan = compute_plan(&target, &[], 16);

        assert_eq!(plan, vec![DeltaOp::Data(target.clone())]);
    }

    // Slice 3: bytes appended after an unchanged prefix → one coalesced Copy of
    // every old block, then a Data op for the new tail.
    #[test]
    fn appended_bytes_copy_prefix_then_data_tail() {
        let bs = 16;
        let old: Vec<u8> = (0..48u8).collect(); // 3 full blocks
        let mut target = old.clone();
        let tail = vec![200, 201, 202, 203, 204];
        target.extend_from_slice(&tail);
        let sigs = sign(&old, bs);

        let plan = compute_plan(&target, &sigs, bs);

        assert_eq!(
            plan,
            vec![DeltaOp::Copy { offset: 0, length: 48 }, DeltaOp::Data(tail)]
        );
        assert_eq!(apply_plan(&old, &plan), target);
    }

    // Slice 4: bytes inserted at the front shift every block off the block
    // boundary. The rolling checksum must still locate the old blocks at their
    // new, non-aligned offsets → leading Data then a coalesced Copy.
    #[test]
    fn prepended_bytes_are_found_at_shifted_offset() {
        let bs = 16;
        let old: Vec<u8> = (0..32u8).collect(); // 2 full blocks
        let head = vec![99, 98, 97];
        let mut target = head.clone();
        target.extend_from_slice(&old);
        let sigs = sign(&old, bs);

        let plan = compute_plan(&target, &sigs, bs);

        assert_eq!(
            plan,
            vec![DeltaOp::Data(head), DeltaOp::Copy { offset: 0, length: 32 }]
        );
        assert_eq!(apply_plan(&old, &plan), target);
    }

    // Slice 5: a changed middle block surfaces as Copy(prefix) + Data(changed) +
    // Copy(suffix); the unchanged blocks on either side are still reused.
    #[test]
    fn changed_middle_block_keeps_surrounding_copies() {
        let bs = 16;
        let old: Vec<u8> = (0..48u8).collect(); // blocks @ 0,16,32
        let changed = vec![0xAA; 16];
        let mut target = old[0..16].to_vec();
        target.extend_from_slice(&changed);
        target.extend_from_slice(&old[32..48]);
        let sigs = sign(&old, bs);

        let plan = compute_plan(&target, &sigs, bs);

        assert_eq!(
            plan,
            vec![
                DeltaOp::Copy { offset: 0, length: 16 },
                DeltaOp::Data(changed),
                DeltaOp::Copy { offset: 32, length: 16 },
            ]
        );
        assert_eq!(apply_plan(&old, &plan), target);
    }

    // Slice 6: consecutive matched blocks collapse into a single Copy and the
    // literal run between two matches stays a single Data op — no fragmentation.
    #[test]
    fn adjacent_blocks_and_literals_coalesce() {
        let bs = 16;
        let old: Vec<u8> = (0..64u8).collect(); // 4 blocks @ 0,16,32,48
        let changed = vec![0xBB; 16];
        // keep blocks 0+1, replace block 2, keep block 3
        let mut target = old[0..32].to_vec();
        target.extend_from_slice(&changed);
        target.extend_from_slice(&old[48..64]);
        let sigs = sign(&old, bs);

        let plan = compute_plan(&target, &sigs, bs);

        assert_eq!(
            plan,
            vec![
                DeltaOp::Copy { offset: 0, length: 32 }, // blocks 0+1 merged
                DeltaOp::Data(changed),
                DeltaOp::Copy { offset: 48, length: 16 },
            ]
        );
        assert_eq!(apply_plan(&old, &plan), target);
    }

    // Slice 7: roundtrip oracle over several mutations — every plan must rebuild
    // its target exactly, and an overlap-heavy edit must send fewer literal
    // bytes than a full upload would.
    #[test]
    fn roundtrip_holds_and_saves_bytes() {
        let bs = 16;
        let old: Vec<u8> = (0..100u8).map(|i| ((i as u32 * 7) % 251) as u8).collect();
        let sigs = sign(&old, bs);

        // (a) single flipped byte in the middle
        let mut flipped = old.clone();
        flipped[50] ^= 0xFF;
        let plan_a = compute_plan(&flipped, &sigs, bs);
        assert_eq!(apply_plan(&old, &plan_a), flipped);

        // (b) appended tail — heavy reuse, must beat a full upload on bytes
        let mut appended = old.clone();
        appended.extend_from_slice(&[1, 2, 3, 4, 5, 6, 7, 8]);
        let plan_b = compute_plan(&appended, &sigs, bs);
        assert_eq!(apply_plan(&old, &plan_b), appended);
        assert!(
            data_bytes(&plan_b) < appended.len(),
            "delta should transmit fewer literal bytes than a full upload"
        );

        // (c) completely different content — still correct, just no savings
        let fresh: Vec<u8> = (0..70u8).map(|i| 255 - i).collect();
        let plan_c = compute_plan(&fresh, &sigs, bs);
        assert_eq!(apply_plan(&old, &plan_c), fresh);
    }

    /// Streaming over a reader must produce the identical plan to the in-memory
    /// path and return the full-file BLAKE3 hash in the same pass — so a large
    /// file can be diffed without ever holding it in memory.
    #[test]
    fn streaming_matches_in_memory_and_hashes() {
        let bs = 16;
        let old: Vec<u8> = (0..200u8).map(|i| ((i as u32 * 13) % 251) as u8).collect();
        let sigs = sign(&old, bs);

        // A middle edit plus an appended tail: forces literals, copies and a
        // shifted region, exercising the sliding-buffer drain across many blocks.
        let mut target = old.clone();
        target[80] ^= 0xFF;
        target.extend_from_slice(&[7, 7, 7, 7, 7]);

        let in_memory = compute_plan(&target, &sigs, bs);

        // READ_CHUNK is far larger than this input, so also drive the matcher
        // through a Cursor to prove the streaming control flow agrees.
        match compute_plan_reader(std::io::Cursor::new(&target), &sigs, bs, usize::MAX).unwrap() {
            StreamingPlan::Delta { ops, hash } => {
                assert_eq!(ops, in_memory, "streaming plan must equal in-memory plan");
                assert_eq!(hash.as_bytes(), blake3::hash(&target).as_bytes());
                assert_eq!(apply_plan(&old, &ops), target);
            }
            StreamingPlan::NotWorthwhile => panic!("unbounded cap must not bail"),
        }
    }

    /// A diff whose literal payload stays under the cap returns a normal plan.
    #[test]
    fn streaming_succeeds_within_cap() {
        let bs = 16;
        let old: Vec<u8> = (0..48u8).collect();
        let sigs = sign(&old, bs);
        let mut target = old.clone();
        target.extend_from_slice(&[1, 2, 3]); // 3 literal bytes, cap 10

        match compute_plan_reader(std::io::Cursor::new(&target), &sigs, bs, 10).unwrap() {
            StreamingPlan::Delta { ops, .. } => assert_eq!(
                ops,
                vec![DeltaOp::Copy { offset: 0, length: 48 }, DeltaOp::Data(vec![1, 2, 3])]
            ),
            StreamingPlan::NotWorthwhile => panic!("3 literal bytes are under the cap"),
        }
    }

    /// When the changed payload exceeds the cap the matcher bails so the caller
    /// falls back to a plain upload — this is what bounds plan memory.
    #[test]
    fn streaming_bails_when_literals_exceed_cap() {
        let bs = 16;
        let old: Vec<u8> = (0..64u8).collect();
        let sigs = sign(&old, bs);
        // Entirely different content of the same length → nothing matches.
        let target: Vec<u8> = (0..64u8).map(|i| 200u8.wrapping_add(i)).collect();

        let out = compute_plan_reader(std::io::Cursor::new(&target), &sigs, bs, 8).unwrap();
        assert!(matches!(out, StreamingPlan::NotWorthwhile));
    }
}
