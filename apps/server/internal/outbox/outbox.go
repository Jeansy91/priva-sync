// Package outbox stores side-effects (storage renames, SSE publishes, mails)
// that were enqueued atomically with a domain mutation. A worker drains the
// table after commit so the side-effect either fires successfully or stays
// retryable across crashes. Ordering within a group_id is strict: seq=1 must
// finish before seq=2 runs. See apps/server/CONTEXT.md → "Outbox".
package outbox

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Kind names the side-effect class. Each kind is owned by a Payload type that
// reports it via Kind(); Register binds that type to its handler in the worker
// registry (see registry.go).
type Kind string

const (
	KindStorageRename  Kind = "storage_rename"
	KindStorageCopy    Kind = "storage_copy"
	KindStorageTrash   Kind = "storage_trash"
	KindStorageUntrash Kind = "storage_untrash"
	KindStorageDelete  Kind = "storage_delete"
	KindSSETyped       Kind = "sse_typed_publish"
	// Mail kinds were split out of a single "mail_send" that second-dispatched
	// on a payload discriminator. Each mail variant now has its
	// own kind so the handler binds to a typed payload; migration 029 rewrote
	// any in-flight mail_send rows onto these.
	KindMailSyncConflict Kind = "mail_sync_conflict"
	KindMailQuotaWarning Kind = "mail_quota_warning"
)

// AllKinds enumerates every kind a producer may enqueue. Registry.Require uses
// it at startup to prove the handler table is complete — a new kind that is
// listed here but never registered fails the boot, not a retry hours later.
var AllKinds = []Kind{
	KindStorageRename,
	KindStorageCopy,
	KindStorageTrash,
	KindStorageUntrash,
	KindStorageDelete,
	KindSSETyped,
	KindMailSyncConflict,
	KindMailQuotaWarning,
}

// Entry is one row in the outbox.
type Entry struct {
	ID            uuid.UUID
	GroupID       uuid.UUID
	Seq           int
	Kind          Kind
	Payload       json.RawMessage
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	DoneAt        *time.Time
	CreatedAt     time.Time
}

// ErrNotFound signals that the requested row is gone (already done, pruned,
// or never existed). Callers treat it as "nothing to do".
var ErrNotFound = errors.New("outbox: entry not found")
