package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"bili2go/internal/bilibili"
)

func TestFetch_FallbackToSecondSource(t *testing.T) {
	want := []byte("second-source-bytes-0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad":
			w.WriteHeader(http.StatusNotFound)
		case "/good":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(want)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	f := NewHTTPFetcher(bilibili.NewClient(""))
	dst := filepath.Join(t.TempDir(), "out.m4s")

	err := f.Fetch(context.Background(), []string{srv.URL + "/bad", srv.URL + "/good"}, dst)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("dst content = %q, want %q", got, want)
	}
}

func TestFetch_AllSourcesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(bilibili.NewClient(""))
	dst := filepath.Join(t.TempDir(), "out.m4s")

	err := f.Fetch(context.Background(), []string{srv.URL + "/a", srv.URL + "/b"}, dst)
	if err == nil {
		t.Fatalf("expected error when all sources fail, got nil")
	}
}

func TestFetch_NoURLs(t *testing.T) {
	f := NewHTTPFetcher(bilibili.NewClient(""))
	dst := filepath.Join(t.TempDir(), "out.m4s")
	if err := f.Fetch(context.Background(), nil, dst); err == nil {
		t.Fatalf("expected error for empty urls, got nil")
	}
}
