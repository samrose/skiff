package registry

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// makeIntegrity computes the sha512 SRI integrity string for the given bytes.
func makeIntegrity(data []byte) string {
	h := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(h[:])
}

func TestDownloadTarball_CorrectIntegrity(t *testing.T) {
	body := []byte("hello tarball content")
	integrity := makeIntegrity(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.Write(body)
	}))
	defer srv.Close()

	got, sha512Hex, err := DownloadTarball(t.Context(), srv.Client(), srv.URL+"/pkg.tgz", integrity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch: got %q, want %q", got, body)
	}
	if len(sha512Hex) != 128 {
		t.Errorf("expected 128-char hex, got %d chars: %s", len(sha512Hex), sha512Hex)
	}
}

func TestDownloadTarball_WrongIntegrity(t *testing.T) {
	body := []byte("hello tarball content")
	wrongIntegrity := makeIntegrity([]byte("some other content entirely"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.Write(body)
	}))
	defer srv.Close()

	_, _, err := DownloadTarball(t.Context(), srv.Client(), srv.URL+"/pkg.tgz", wrongIntegrity)
	if !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("expected ErrIntegrityMismatch, got %v", err)
	}
}

func TestDownloadTarball_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, _, err := DownloadTarball(t.Context(), srv.Client(), srv.URL+"/missing.tgz", "sha512-AAAA")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestDownloadTarball_EmptyBody(t *testing.T) {
	// Empty body with correct (empty) integrity.
	body := []byte{}
	integrity := makeIntegrity(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	got, sha512Hex, err := DownloadTarball(t.Context(), srv.Client(), srv.URL+"/empty.tgz", integrity)
	if err != nil {
		t.Fatalf("unexpected error for empty body: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty body, got %d bytes", len(got))
	}
	if len(sha512Hex) != 128 {
		t.Errorf("expected 128-char sha512 hex, got %d", len(sha512Hex))
	}
}
