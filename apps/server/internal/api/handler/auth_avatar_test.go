package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

// pngFixture encodes a tiny valid PNG so PutAvatar's decode step succeeds.
func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	return buf.Bytes()
}

// multipartAvatarBody builds a multipart/form-data body with a single
// "avatar" file field, mirroring what the desktop/web clients send.
func multipartAvatarBody(t *testing.T, data []byte) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, w.FormDataContentType()
}

func TestPutAvatar_StoresProcessedPNGAndSetsPath(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body, ct := multipartAvatarBody(t, pngFixture(t))
	r := reqWithClaims("PUT", "/api/v1/auth/avatar", body, claims)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.PutAvatar(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := "/api/v1/auth/avatar/" + claims.UserID.String()
	if resp.AvatarURL != want {
		t.Errorf("avatar_url = %q, want %q", resp.AvatarURL, want)
	}

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.AvatarPath == nil || *u.AvatarPath != claims.UserID.String()+".png" {
		t.Errorf("avatar_path = %v, want %s.png", u.AvatarPath, claims.UserID)
	}
}

func TestPutAvatar_RejectsMissingField(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.Close()
	r := reqWithClaims("PUT", "/api/v1/auth/avatar", body, claims)
	r.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.PutAvatar(rec, r)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != ErrAvatarMissing {
		t.Errorf("code = %v, want %q", resp["code"], ErrAvatarMissing)
	}
}

func TestPutAvatar_RejectsInvalidImage(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body, ct := multipartAvatarBody(t, []byte("not-an-image"))
	r := reqWithClaims("PUT", "/api/v1/auth/avatar", body, claims)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.PutAvatar(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrAvatarInvalid {
		t.Errorf("code = %v, want %q", resp["code"], ErrAvatarInvalid)
	}
}

func TestDeleteAvatar_ClearsPathAndFile(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	// Seed an avatar first via the real upload path.
	body, ct := multipartAvatarBody(t, pngFixture(t))
	r := reqWithClaims("PUT", "/api/v1/auth/avatar", body, claims)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	h.PutAvatar(w, r)
	if w.Code != 200 {
		t.Fatalf("setup PutAvatar: status=%d body=%s", w.Code, w.Body.String())
	}

	dr := reqWithClaims("DELETE", "/api/v1/auth/avatar", nil, claims)
	dw := httptest.NewRecorder()
	h.DeleteAvatar(dw, dr)
	if dw.Code != 204 {
		t.Fatalf("delete status = %d, want 204; body=%s", dw.Code, dw.Body.String())
	}

	u, err := env.users.FindByID(context.Background(), claims.UserID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if u.AvatarPath != nil {
		t.Errorf("avatar_path = %v, want nil after delete", u.AvatarPath)
	}
}

func TestGetAvatar_ReturnsPNGBytes(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	body, ct := multipartAvatarBody(t, pngFixture(t))
	pr := reqWithClaims("PUT", "/api/v1/auth/avatar", body, claims)
	pr.Header.Set("Content-Type", ct)
	pw := httptest.NewRecorder()
	h.PutAvatar(pw, pr)
	if pw.Code != 200 {
		t.Fatalf("setup PutAvatar: status=%d body=%s", pw.Code, pw.Body.String())
	}

	r := reqWithClaims("GET", "/api/v1/auth/avatar/"+claims.UserID.String(), nil, claims)
	r = withURLParams(r, "userID", claims.UserID.String())
	w := httptest.NewRecorder()
	h.GetAvatar(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("empty avatar body")
	}
}

func TestGetAvatar_InvalidUserID(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims("GET", "/api/v1/auth/avatar/not-a-uuid", nil, claims)
	r = withURLParams(r, "userID", "not-a-uuid")
	w := httptest.NewRecorder()
	h.GetAvatar(w, r)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrInvalidUserID {
		t.Errorf("code = %v, want %q", resp["code"], ErrInvalidUserID)
	}
}

func TestGetAvatar_NotFoundWhenNoneSet(t *testing.T) {
	h, env := newAuthTestEnv(t)
	claims := createTestUser(t, env)

	r := reqWithClaims("GET", "/api/v1/auth/avatar/"+claims.UserID.String(), nil, claims)
	r = withURLParams(r, "userID", claims.UserID.String())
	w := httptest.NewRecorder()
	h.GetAvatar(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != ErrAvatarNotPresent {
		t.Errorf("code = %v, want %q", resp["code"], ErrAvatarNotPresent)
	}
}
