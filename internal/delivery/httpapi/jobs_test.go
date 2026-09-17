package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bili2go/internal/analyze"
	"bili2go/internal/app"
	"bili2go/internal/jobstore"
)

func jobServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := jobstore.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	s := New(dl, cfg)
	s.SetAnalyzer(analyze.NewAnalyzer(fakeDigester{}, fakeSummarizer{}))
	s.SetJobStore(store, 2)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.StartJobWorkers(ctx)
	return s.Handler()
}

func TestJobsAsyncLifecycle(t *testing.T) {
	h := jobServer(t)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/jobs?bvid=BV1", nil))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("create → %d, want 202 (%s)", rr.Code, rr.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(rr.Body.Bytes(), &created)
	if created.ID == "" || created.Status != "queued" {
		t.Fatalf("create body = %+v", created)
	}

	var job jobstore.Job
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rr2 := httptest.NewRecorder()
		h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/jobs/"+created.ID, nil))
		if rr2.Code != http.StatusOK {
			t.Fatalf("get → %d", rr2.Code)
		}
		json.Unmarshal(rr2.Body.Bytes(), &job)
		if job.Status == jobstore.StatusDone || job.Status == jobstore.StatusError {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != jobstore.StatusDone {
		t.Fatalf("job final status = %q, want done", job.Status)
	}
	var res analyzeResp
	json.Unmarshal(job.Result, &res)
	if res.Markdown == "" || res.Engine != "whisper" {
		t.Errorf("job result = %+v", res)
	}

	// 列表包含该任务
	rr3 := httptest.NewRecorder()
	h.ServeHTTP(rr3, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	var list []jobstore.Job
	json.Unmarshal(rr3.Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v", list)
	}

	// 未知任务 → 404
	rr4 := httptest.NewRecorder()
	h.ServeHTTP(rr4, httptest.NewRequest(http.MethodGet, "/api/jobs/nope", nil))
	if rr4.Code != http.StatusNotFound {
		t.Errorf("unknown job → %d, want 404", rr4.Code)
	}
}

func TestJobsNotConfigured(t *testing.T) {
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	s := New(dl, cfg)
	s.SetAnalyzer(analyze.NewAnalyzer(fakeDigester{}, fakeSummarizer{}))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/jobs?bvid=BV1", nil))
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("jobs without store → %d, want 501", rr.Code)
	}
}
