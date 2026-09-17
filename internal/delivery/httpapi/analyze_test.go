package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bili2go/internal/analyze"
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
