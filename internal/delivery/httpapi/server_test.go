package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"bili2go/internal/app"
	"bili2go/internal/domain"
)

type fakeMeta struct{}

func (fakeMeta) View(ctx context.Context, bvid string) (domain.Video, error) {
	return domain.Video{
		AID: 100, BVID: bvid, Title: "标题/带斜杠",
		Pages: []domain.Page{{CID: 11, Index: 1, Title: "p1", Duration: 60 * time.Second}},
	}, nil
}

type fakeStreams struct{}

func (fakeStreams) Manifest(ctx context.Context, aid, cid int64, qn int) (domain.Manifest, error) {
	return domain.Manifest{
		Available: []domain.Quality{domain.NewQuality(32), domain.NewQuality(16)},
		Videos:    []domain.Stream{{Kind: domain.StreamVideo, Quality: domain.NewQuality(32), Codec: domain.CodecAVC, BandWidth: 100, URLs: []string{"v"}}},
		Audios:    []domain.Stream{{Kind: domain.StreamAudio, BandWidth: 50, URLs: []string{"a"}}},
	}, nil
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(ctx context.Context, urls []string, dst string) error {
	return os.WriteFile(dst, []byte("x"), 0o644)
}

type fakeMuxer struct{}

func (fakeMuxer) Mux(ctx context.Context, v, a, dst string) error {
	return os.WriteFile(dst, []byte("MP4DATA"), 0o644)
}

func testServer() *Server {
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	return New(dl)
}

func TestInfoJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/info?bvid=BV1&qn=80", nil)
	testServer().Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["aid"].(float64) != 100 {
		t.Errorf("aid = %v, want 100", body["aid"])
	}
	if qs := body["qualities"].([]any); len(qs) != 2 {
		t.Errorf("qualities len = %d, want 2", len(qs))
	}
	cur := body["current"].(map[string]any)
	if cur["qn"].(float64) != 32 {
		t.Errorf("current.qn = %v, want 32 (downgraded from 80)", cur["qn"])
	}
	if cur["codec"].(string) != "AVC" {
		t.Errorf("current.codec = %v, want AVC", cur["codec"])
	}
}

func TestDownloadStreamsMP4(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/download?bvid=BV1&qn=80", nil)
	testServer().Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}
	if q := rr.Header().Get("X-Bili-Quality"); q != "32" {
		t.Errorf("X-Bili-Quality = %q, want 32", q)
	}
	if c := rr.Header().Get("X-Bili-Codec"); c != "AVC" {
		t.Errorf("X-Bili-Codec = %q, want AVC", c)
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, ".mp4") || strings.ContainsAny(cd, "/\\") {
		t.Errorf("Content-Disposition = %q, want sanitized .mp4 filename", cd)
	}
	if rr.Body.String() != "MP4DATA" {
		t.Errorf("body = %q, want MP4DATA", rr.Body.String())
	}
}

func TestMissingBVID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	testServer().Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
