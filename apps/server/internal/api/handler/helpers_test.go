package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorIncludesCodeAndMessage(t *testing.T) {
	rr := httptest.NewRecorder()

	writeError(rr, 401, "UNAUTHORIZED", "missing token")

	if rr.Code != 401 {
		t.Fatalf("status: got %d want 401", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "UNAUTHORIZED" {
		t.Errorf("code: got %q want UNAUTHORIZED", body["code"])
	}
	if body["error"] != "missing token" {
		t.Errorf("error: got %q want %q", body["error"], "missing token")
	}
}
