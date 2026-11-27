// Package errcode holds the API error codes that more than one layer needs to
// name. Most codes live with the handler that writes them; these do not,
// because the chunked-upload assembly runs outside the request and
// records its verdict on the session row. Both the worker that writes a code
// and the handler that answers with it must mean the same string — the web UI
// translates by code (packages/i18n, errors.*).
package errcode

const (
	AssemblyFailed      = "ASSEMBLY_FAILED"
	UploadFailed        = "UPLOAD_FAILED"
	ContentHashMismatch = "CONTENT_HASH_MISMATCH"
	ChunksMissing       = "CHUNKS_MISSING"
	AssembledSizeWrong  = "ASSEMBLED_SIZE_MISMATCH"
	QuotaExceeded       = "QUOTA_EXCEEDED"
	// AssemblyAbandoned is the verdict on a session that ran out of attempts
	// without ever reaching one of the others — repeated I/O trouble, or a
	// worker killed mid-assembly often enough.
	AssemblyAbandoned = "ASSEMBLY_ABANDONED"
)
