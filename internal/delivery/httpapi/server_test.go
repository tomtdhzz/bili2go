package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0 // 隔离：既有测试不走缓存
	return New(dl, cfg)
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

type blockingFetcher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingFetcher) Fetch(ctx context.Context, urls []string, dst string) error {
	f.once.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return os.WriteFile(dst, []byte("x"), 0o644)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func saturatedServer() (*blockingFetcher, http.Handler) {
	bf := &blockingFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, bf, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.MaxConcurrent = 1
	cfg.MaxWait = 0
	cfg.CacheMaxBytes = 0 // 限流测试不走缓存
	return bf, New(dl, cfg).Handler()
}

// AC-A1：单槽被占且不排队时，新 /download 返回 429 + Retry-After。
func TestDownloadBusyReturns429(t *testing.T) {
	bf, h := saturatedServer()
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/download?bvid=BV1&qn=80", nil))
	}()
	<-bf.entered // 槽位已占（进入 fetch）
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/download?bvid=BV1&qn=80", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Errorf("missing Retry-After header")
	}
	close(bf.release)
}

// AC-A5：下载饱和时 /api/info 仍可用。
func TestInfoNotLimitedByDownloads(t *testing.T) {
	bf, h := saturatedServer()
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/download?bvid=BV1&qn=80", nil))
	}()
	<-bf.entered
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/info?bvid=BV1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/api/info status = %d during saturation, want 200", rr.Code)
	}
	close(bf.release)
}

type countingMuxer struct{ n int32 }

func (m *countingMuxer) Mux(ctx context.Context, v, a, dst string) error {
	atomic.AddInt32(&m.n, 1)
	return os.WriteFile(dst, []byte("MP4DATA"), 0o644)
}

// AC-C1：相同请求二次 → 第二次自缓存回传，不再触发上游(muxer)；两次 body 相同。
func TestDownloadCacheHit(t *testing.T) {
	mux := &countingMuxer{}
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, mux, nil)
	cfg := DefaultConfig()
	cfg.CacheDir = t.TempDir()
	cfg.CacheMaxBytes = 1 << 30
	h := New(dl, cfg).Handler()

	do := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/download?bvid=BV1&qn=80", nil))
		return rr
	}
	r1 := do()
	r2 := do()
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("codes r1=%d r2=%d", r1.Code, r2.Code)
	}
	if got := atomic.LoadInt32(&mux.n); got != 1 {
		t.Errorf("mux calls = %d, want 1 (2nd served from cache)", got)
	}
	if r1.Body.String() != "MP4DATA" || r2.Body.String() != "MP4DATA" {
		t.Errorf("bodies: r1=%q r2=%q", r1.Body.String(), r2.Body.String())
	}
	if r2.Header().Get("X-Bili-Quality") != "32" {
		t.Errorf("cached X-Bili-Quality = %q, want 32", r2.Header().Get("X-Bili-Quality"))
	}
}
