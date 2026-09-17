package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"bili2go/internal/analyze"
	"bili2go/internal/app"
)

type fakeDigester struct{}

func (fakeDigester) Digest(ctx context.Context, videoPath, outDir string) (analyze.DigestOutput, error) {
	return analyze.DigestOutput{
		Dir:  outDir,
		JSON: []byte(`{"source":{},"transcript":{},"screen_keywords":[],"gaps":[]}`),
		Meta: analyze.DigestMeta{Engine: "whisper", Segments: 3, DurationS: 39},
	}, nil
}

type fakeSummarizer struct{}

func (fakeSummarizer) Summarize(ctx context.Context, digestJSON []byte) (analyze.Summary, error) {
	return analyze.Summary{Markdown: "## ① ok\n", Model: "fallback", Fallback: true}, nil
}

func analyzeServer() *Server {
	s := testServer()
	s.SetAnalyzer(analyze.NewAnalyzer(fakeDigester{}, fakeSummarizer{}))
	return s
}

func TestAnalyzeOK(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/analyze?bvid=BV1", nil)
	analyzeServer().Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var resp analyzeResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Markdown != "## ① ok\n" || resp.Model != "fallback" || !resp.Fallback {
		t.Errorf("summary fields = %+v", resp)
	}
	if resp.Engine != "whisper" || resp.Segments != 3 || resp.Title != "标题/带斜杠" {
		t.Errorf("meta fields = %+v", resp)
	}
}

func TestAnalyzeMissingBVID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/analyze", nil)
	analyzeServer().Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestAnalyzeNotConfigured(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/analyze?bvid=BV1", nil)
	testServer().Handler().ServeHTTP(rr, req) // 无 SetAnalyzer
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rr.Code)
	}
}

func TestAnalyzePersistsArtifacts(t *testing.T) {
	dir := t.TempDir()
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	cfg.ArtifactDir = dir
	s := New(dl, cfg)
	s.SetAnalyzer(analyze.NewAnalyzer(fakeDigester{}, fakeSummarizer{}))

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/analyze?bvid=BV1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var resp analyzeResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.VideoPath == "" || resp.SummaryPath == "" {
		t.Fatalf("expected persisted paths, got %+v", resp)
	}
	if !strings.HasPrefix(resp.VideoPath, dir) {
		t.Errorf("video_path %q not under artifact dir %q", resp.VideoPath, dir)
	}
	for _, p := range []string{resp.VideoPath, resp.SummaryPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("artifact missing on disk: %s (%v)", p, err)
		}
	}
}

type countingDigester struct{ n *int32 }

func (c countingDigester) Digest(ctx context.Context, videoPath, outDir string) (analyze.DigestOutput, error) {
	atomic.AddInt32(c.n, 1)
	return analyze.DigestOutput{
		Dir:  outDir,
		JSON: []byte(`{"source":{},"transcript":{},"screen_keywords":[],"gaps":[]}`),
		Meta: analyze.DigestMeta{Engine: "whisper", Segments: 1},
	}, nil
}

// TestAnalyzeReusesArtifacts：-artifact-dir 下同一 bvid 二次请求命中 result.json，不再 digest。
func TestAnalyzeReusesArtifacts(t *testing.T) {
	dir := t.TempDir()
	var digestCalls int32
	dl := app.NewDownloader(fakeMeta{}, fakeStreams{}, fakeFetcher{}, fakeMuxer{}, nil)
	cfg := DefaultConfig()
	cfg.CacheMaxBytes = 0
	cfg.ArtifactDir = dir
	s := New(dl, cfg)
	s.SetAnalyzer(analyze.NewAnalyzer(countingDigester{&digestCalls}, fakeSummarizer{}))

	do := func() analyzeResp {
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/analyze?bvid=BVX", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
		}
		var resp analyzeResp
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	first := do()
	if first.Cached {
		t.Errorf("first request should not be cached")
	}
	second := do()
	if !second.Cached {
		t.Errorf("second request should be served from artifacts (cached=true)")
	}
	if got := atomic.LoadInt32(&digestCalls); got != 1 {
		t.Errorf("digest calls = %d, want 1 (2nd request must reuse)", got)
	}
	if second.Markdown != first.Markdown {
		t.Errorf("cached markdown differs from original")
	}
}
