package sync

import (
	"context"
	"errors"
	"os"

	"github.com/google/uuid"

	"privasync/server/internal/mail"
	"privasync/server/internal/outbox"
	"privasync/server/internal/repository"
	"privasync/server/internal/sse"
	"privasync/server/internal/storage"
)

// Outbox payloads. Each implements outbox.Payload so the registry binds it to
// its Kind by type — a coordinator that drifts a payload shape breaks at
// compile time, not at retry time.
//
// Rename/Copy/Trash/Untrash carry the same {user_id,root_id,from,to} shape but
// stay separate types on purpose: they are distinct storage operations (move
// vs. copy) and each owns its own schema, so one growing a field never
// disturbs the others.

// RenamePayload moves a file to a new path inside one sync root.
type RenamePayload struct {
	UserID uuid.UUID `json:"user_id"`
	RootID uuid.UUID `json:"root_id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

func (RenamePayload) Kind() outbox.Kind { return outbox.KindStorageRename }

// CopyPayload copies a file's bytes to a second path (conflict-copy).
type CopyPayload struct {
	UserID uuid.UUID `json:"user_id"`
	RootID uuid.UUID `json:"root_id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

func (CopyPayload) Kind() outbox.Kind { return outbox.KindStorageCopy }

// TrashPayload moves a file from its live path into the trash area.
type TrashPayload struct {
	UserID uuid.UUID `json:"user_id"`
	RootID uuid.UUID `json:"root_id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

func (TrashPayload) Kind() outbox.Kind { return outbox.KindStorageTrash }

// UntrashPayload restores a file from the trash area back to a live path.
type UntrashPayload struct {
	UserID uuid.UUID `json:"user_id"`
	RootID uuid.UUID `json:"root_id"`
	From   string    `json:"from"`
	To     string    `json:"to"`
}

func (UntrashPayload) Kind() outbox.Kind { return outbox.KindStorageUntrash }

// DeletePayload removes a single trashed file from disk. The DB row is
// already gone by the time the worker drains this entry — the handler only
// touches storage. Missing-on-disk is tolerated so a crash between
// PurgeTrashRecord and storage-delete stays self-healing.
type DeletePayload struct {
	UserID    uuid.UUID `json:"user_id"`
	RootID    uuid.UUID `json:"root_id"`
	TrashPath string    `json:"trash_path"`
}

func (DeletePayload) Kind() outbox.Kind { return outbox.KindStorageDelete }

// BuildHandlers returns the worker registry for all kinds the coordinator
// emits. Wired once in main.go.
func BuildHandlers(stor *storage.Service, hub *sse.Hub, mailer *mail.Mailer, users *repository.UserRepository, ob *outbox.Repository, baseURL string) outbox.Registry {
	reg := outbox.Registry{}

	outbox.Register(reg, func(_ context.Context, p RenamePayload) error {
		return stor.MoveFile(p.UserID, p.RootID.String(), p.From, p.To)
	})
	outbox.Register(reg, func(_ context.Context, p CopyPayload) error {
		return stor.CopyPath(p.UserID, p.RootID.String(), p.From, p.To)
	})
	outbox.Register(reg, func(_ context.Context, p TrashPayload) error {
		return stor.MoveFile(p.UserID, p.RootID.String(), p.From, p.To)
	})
	outbox.Register(reg, func(_ context.Context, p UntrashPayload) error {
		return stor.MoveFile(p.UserID, p.RootID.String(), p.From, p.To)
	})
	outbox.Register(reg, func(_ context.Context, p DeletePayload) error {
		if err := stor.DeleteTrash(p.UserID, p.RootID.String(), p.TrashPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})

	reg[outbox.KindSSETyped] = TypedSSEHandler(hub)
	registerMailHandlers(reg, mailer, users, ob, baseURL)

	return reg
}
