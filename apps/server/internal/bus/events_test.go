package bus_test

import (
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/bus"
)

func TestFilePurgedByCron_EventName(t *testing.T) {
	e := bus.FilePurgedByCron{
		UserID: uuid.New(), RootID: uuid.New(), FileID: uuid.New(),
		Bytes: 1024,
	}
	if got := e.EventName(); got != bus.EventFilePurgedByCron {
		t.Fatalf("EventName = %q, want %q", got, bus.EventFilePurgedByCron)
	}
	if bus.EventFilePurgedByCron != "file.purged_by_cron" {
		t.Fatalf("constant drift: %q", bus.EventFilePurgedByCron)
	}
}

func TestAdminQuotaAdjusted_EventName(t *testing.T) {
	e := bus.AdminQuotaAdjusted{
		UserID:   uuid.New(),
		NewUsed:  100,
		NewTrash: 50,
	}
	if got := e.EventName(); got != bus.EventAdminQuotaAdjusted {
		t.Fatalf("EventName = %q, want %q", got, bus.EventAdminQuotaAdjusted)
	}
	if bus.EventAdminQuotaAdjusted != "quota.admin_adjusted" {
		t.Fatalf("constant drift: %q", bus.EventAdminQuotaAdjusted)
	}
}
