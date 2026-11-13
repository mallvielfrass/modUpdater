package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestUsage(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	code := run(nil)
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if code != 2 || strings.TrimSpace(buf.String()) != usageLine {
		t.Fatalf("%d %q", code, buf.String())
	}
}
