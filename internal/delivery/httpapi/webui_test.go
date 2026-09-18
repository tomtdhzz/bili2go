package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bili2go/internal/app"
	"bili2go/internal/jobstore"
)

func kbServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := jobstore.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Create(jobstore.Job{
		ID: "job1", BVID: "BV1", Status: jobstore.StatusDone, CreatedAt: time.Now(),
		Result: json.RawMessage(`{"title":"第三季度增长复盘","markdown":"## ① 一句话结论\nGMV 同比 38%","engine":"whisper","segments":3,"duration_s":93}`),
	})
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	s := New(dl, cfg)
	s.SetJobStore(store, 1)
	return s.Handler()
}

func TestKBIndexAndSearch(t *testing.T) {
	h := kbServer(t)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/kb → %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "BV1") || !strings.Contains(body, "第三季度增长复盘") {
		t.Errorf("/kb missing job listing")
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}

	// 命中检索
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb?q=复盘", nil))
	if !strings.Contains(rr.Body.String(), "BV1") {
		t.Errorf("search 复盘 should list BV1")
	}
	// 未命中
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb?q=不存在zzz", nil))
	if strings.Contains(rr.Body.String(), ">BV1<") {
		t.Errorf("no-match should not list BV1")
	}
}

func TestKBDetail(t *testing.T) {
	h := kbServer(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb/job1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/kb/job1 → %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "GMV 同比 38%") || !strings.Contains(body, "第三季度增长复盘") {
		t.Errorf("detail missing summary content")
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb/nope", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown /kb/{id} → %d, want 404", rr.Code)
	}
}

func TestKBDisabled(t *testing.T) {
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	rr := httptest.NewRecorder()
	New(dl, cfg).Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/kb", nil))
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("/kb without -jobs-dir → %d, want 501", rr.Code)
	}
}
