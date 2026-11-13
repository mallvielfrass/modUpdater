package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadErrReleased(t *testing.T) {
	err := uploadErr("0.1.0", http.StatusConflict, "conflict")
	want := "версия 0.1.0 уже выпущена, укажите новую через -version\nHTTP 409 эта версия уже выпущена"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
}

func TestUploadErrSpecificCode(t *testing.T) {
	err := uploadErr("0.1.0", http.StatusConflict, "released")
	if err == nil || err.Error() != "HTTP 409 released" {
		t.Fatalf("got %v", err)
	}
}

func TestUploadReleaseConflictBody(t *testing.T) {
	dir := t.TempDir()
	linux := filepath.Join(dir, "linux")
	windows := filepath.Join(dir, "windows")
	if err := os.WriteFile(linux, []byte("L"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(windows, []byte("W"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"version_released"}`)
	}))
	defer srv.Close()
	err := uploadRelease(srv.Client(), srv.URL, "tok", "0.1.0", linux, windows)
	if err == nil || err.Error() != "HTTP 409 version_released" {
		t.Fatalf("got %v", err)
	}
}
