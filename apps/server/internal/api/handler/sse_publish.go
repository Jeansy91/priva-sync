package handler

import (
	"encoding/hex"

	"privasync/server/internal/model"
	sseevents "privasync/server/internal/sse/events"
)

// sseEventsFile lifts a model.File row into the typed SSE File struct so
// handler-side publishes share the wire shape with the coordinator's outbox
// publishes (ContentHash is hex on the wire, raw bytes in the model).
func sseEventsFile(f *model.File) sseevents.File {
	return sseevents.File{
		ID:            f.ID,
		Path:          f.Path,
		Size:          f.Size,
		MTime:         f.MTime,
		ContentHash:   hex.EncodeToString(f.ContentHash),
		IsDeleted:     f.IsDeleted,
		IsDir:         f.IsDir,
		ServerVersion: f.ServerVersion,
		UpdatedAt:     f.UpdatedAt,
	}
}
