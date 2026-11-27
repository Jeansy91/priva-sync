package handler

import (
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

func TestRoleError_Error(t *testing.T) {
	e := &roleError{Status: 400, Code: ErrInvalidBody, Msg: "invalid role"}
	if e.Error() != "invalid role" {
		t.Errorf("Error() = %q, want %q", e.Error(), "invalid role")
	}
}

func TestValidateActiveChange(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()
	cases := []struct {
		name    string
		active  bool
		target  uuid.UUID
		caller  uuid.UUID
		wantErr string
	}{
		{"lock other", false, other, caller, ""},
		{"unlock other", true, other, caller, ""},
		{"unlock self ok", true, caller, caller, ""},
		{"lock self denied", false, caller, caller, ErrInvalidTargetUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateActiveChange(c.active, c.target, c.caller)
			if c.wantErr == "" && err != nil {
				t.Errorf("got err %v, want nil", err)
			}
			if c.wantErr != "" && (err == nil || err.Code != c.wantErr) {
				t.Errorf("got err %v, want code %q", err, c.wantErr)
			}
		})
	}
}

func TestValidateRoleChange(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()

	cases := []struct {
		name    string
		newRole model.Role
		target  uuid.UUID
		caller  uuid.UUID
		wantErr string
	}{
		{"valid promotion", model.RoleAdmin, other, caller, ""},
		{"valid demotion", model.RoleUser, other, caller, ""},
		{"invalid role", model.Role("root"), other, caller, ErrInvalidBody},
		{"empty role", model.Role(""), other, caller, ErrInvalidBody},
		{"self-demotion", model.RoleUser, caller, caller, ErrInvalidTargetUser},
		{"self-promotion ok (no-op)", model.RoleAdmin, caller, caller, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateRoleChange(c.newRole, c.target, c.caller)
			if c.wantErr == "" && err != nil {
				t.Errorf("got err %v, want nil", err)
			}
			if c.wantErr != "" && (err == nil || err.Code != c.wantErr) {
				t.Errorf("got err %v, want code %q", err, c.wantErr)
			}
		})
	}
}
