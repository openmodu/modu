package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunFetchPrintsMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>CLI Page</title></head><body><article><h1>CLI Title</h1><p>Body text.</p></article></body></html>`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := runFetch(context.Background(), server.URL, fetchCLIOptions{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "# CLI Title") || !strings.Contains(got, "Body text.") {
		t.Fatalf("unexpected markdown output:\n%s", got)
	}
}

func TestRunFetchPrintsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>CLI JSON</title></head><body><article><p>JSON body.</p></article></body></html>`))
	}))
	defer server.Close()

	var out bytes.Buffer
	err := runFetch(context.Background(), server.URL, fetchCLIOptions{json: true}, &out)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"title": "CLI JSON"`, `"content":`, "JSON body."} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in JSON output:\n%s", want, got)
		}
	}
}
