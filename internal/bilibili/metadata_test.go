package bilibili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractBVID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"BV1NKNv69EZz", "BV1NKNv69EZz"},
		{"https://www.bilibili.com/video/BV1NKNv69EZz", "BV1NKNv69EZz"},
		{"https://www.bilibili.com/video/BV1NKNv69EZz/?p=2", "BV1NKNv69EZz"},
	}
	for _, c := range cases {
		got, err := extractBVID(c.in)
		if err != nil {
			t.Fatalf("extractBVID(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("extractBVID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := extractBVID("no-id-here"); err == nil {
		t.Fatalf("extractBVID with no id: expected error")
	}
}

const viewJSON = `{"code":0,"message":"OK","data":{"aid":100,"bvid":"BV1NKNv69EZz","title":"t","pages":[{"cid":11,"page":1,"part":"p1","duration":1494},{"cid":22,"page":2,"part":"p2","duration":60}]}}`

func TestMetadataView(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("bvid"); got != "BV1NKNv69EZz" {
			t.Errorf("bvid query = %q", got)
		}
		w.Write([]byte(viewJSON))
	}))
	defer srv.Close()

	m := NewMetadata(NewClient(""))
	m.base = srv.URL

	v, err := m.View(context.Background(), "https://www.bilibili.com/video/BV1NKNv69EZz")
	if err != nil {
		t.Fatalf("View error: %v", err)
	}
	if v.AID != 100 || v.BVID != "BV1NKNv69EZz" || v.Title != "t" {
		t.Fatalf("video header wrong: %+v", v)
	}
	if len(v.Pages) != 2 {
		t.Fatalf("pages len = %d, want 2", len(v.Pages))
	}
	p2 := v.Pages[1]
	if p2.CID != 22 {
		t.Fatalf("p2 CID = %d, want 22", p2.CID)
	}
	if p2.Index != 2 {
		t.Fatalf("p2 Index = %d, want 2", p2.Index)
	}
	if p2.Duration != 60*time.Second {
		t.Fatalf("p2 Duration = %v, want 60s", p2.Duration)
	}
	if p2.Title != "p2" {
		t.Fatalf("p2 Title = %q, want p2", p2.Title)
	}
}
