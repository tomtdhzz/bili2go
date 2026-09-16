package analyze

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureDigest = `{
  "schema_version": "1",
  "source": {"duration_s": 39, "has_audio": true},
  "transcript": {"engine": "SpeechTranscriber", "segments": [{"start_s":0,"end_s":13,"text":"a"}], "text": "a"},
  "screen_keywords": [{"term":"退款率 1.7%","score":1.3,"first_t_s":14,"spoken_in_transcript":false,"is_chrome":false,"evidence_frames":[1],"occurrences":1}],
  "gaps": []
}`

// fakeDigester 返回预置 digest，避免依赖 macOS 原生 video-digest。
type fakeDigester struct {
	json    string
	wantDir string
	t       *testing.T
}

func (f fakeDigester) Digest(_ context.Context, videoPath, outDir string) (DigestOutput, error) {
	if f.wantDir != "" && outDir != f.wantDir {
		f.t.Fatalf("outDir = %q, want %q", outDir, f.wantDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return DigestOutput{}, err
	}
	raw := []byte(f.json)
	meta, err := parseMeta(raw)
	if err != nil {
		return DigestOutput{}, err
	}
	return DigestOutput{Dir: outDir, DigestPath: filepath.Join(outDir, "digest.json"), JSON: raw, Meta: meta}, nil
}

type fakeSummarizer struct {
	got []byte
	ret Summary
}

func (f *fakeSummarizer) Summarize(_ context.Context, digestJSON []byte) (Summary, error) {
	f.got = digestJSON
	return f.ret, nil
}

func TestAnalyzeWritesSummaryAndForwardsDigest(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "v.digest")
	sum := &fakeSummarizer{ret: Summary{Markdown: "# hi\n", Model: "fallback", Fallback: true}}
	a := NewAnalyzer(fakeDigester{json: fixtureDigest, wantDir: outDir, t: t}, sum)

	rep, err := a.Analyze(context.Background(), filepath.Join(dir, "v.mp4"), outDir)
	if err != nil {
		t.Fatal(err)
	}
	// digest.json 原样转发（未重序列化）。
	if string(sum.got) != fixtureDigest {
		t.Errorf("forwarded digest mismatch:\n got %s", sum.got)
	}
	// summary.md 落在产物目录。
	wantPath := filepath.Join(outDir, "summary.md")
	if rep.SummaryPath != wantPath {
		t.Errorf("SummaryPath = %q, want %q", rep.SummaryPath, wantPath)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# hi\n" {
		t.Errorf("summary.md = %q", got)
	}
	// Report 元信息来自 digest。
	if rep.Digest.Meta.DurationS != 39 || rep.Digest.Meta.Engine != "SpeechTranscriber" || rep.Digest.Meta.Keywords != 1 {
		t.Errorf("meta = %+v", rep.Digest.Meta)
	}
	if !rep.Summary.Fallback {
		t.Errorf("expected fallback summary")
	}
}

func TestAnalyzeDefaultDigestDir(t *testing.T) {
	got := DefaultDigestDir("/tmp/movies/out.mp4")
	want := filepath.Join("/tmp/movies", "out.digest")
	if got != want {
		t.Errorf("DefaultDigestDir = %q, want %q", got, want)
	}
}

func TestHTTPSummarizerRoundTrip(t *testing.T) {
	var gotBody []byte
	var gotTop string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/summarize" {
			t.Errorf("path = %s", r.URL.Path)
		}
		gotTop = r.URL.Query().Get("top")
		gotBody, _ = io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(map[string]any{
			"markdown": "## ① ok\n", "model": "fallback", "fallback": true,
		})
	}))
	defer srv.Close()

	s := NewHTTPSummarizer(srv.URL, 10, "")
	out, err := s.Summarize(context.Background(), []byte(fixtureDigest))
	if err != nil {
		t.Fatal(err)
	}
	if gotTop != "10" {
		t.Errorf("top = %q, want 10", gotTop)
	}
	if string(gotBody) != fixtureDigest {
		t.Errorf("body mismatch")
	}
	if out.Markdown != "## ① ok\n" || !out.Fallback {
		t.Errorf("out = %+v", out)
	}
}

func TestHTTPSummarizerSendsToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"markdown": "## ① ok\n", "model": "fallback", "fallback": true})
	}))
	defer srv.Close()

	// 带正确 token → 服务端 200
	if _, err := NewHTTPSummarizer(srv.URL, 0, "s3cret").Summarize(context.Background(), []byte(fixtureDigest)); err != nil {
		t.Fatalf("with token: %v", err)
	}
	if gotAuth != "Bearer s3cret" {
		t.Errorf("Authorization = %q, want Bearer s3cret", gotAuth)
	}
	// 空 token → 服务端 401 → 客户端报错
	if _, err := NewHTTPSummarizer(srv.URL, 0, "").Summarize(context.Background(), []byte(fixtureDigest)); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("without token err = %v, want 401 propagated", err)
	}
}

func TestHTTPSummarizerErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "digest 缺少必需字段: ['gaps']"})
	}))
	defer srv.Close()

	s := NewHTTPSummarizer(srv.URL, 0, "")
	_, err := s.Summarize(context.Background(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "缺少必需字段") {
		t.Errorf("err = %v, want propagated service error", err)
	}
}

func TestParseMetaSilent(t *testing.T) {
	raw := []byte(`{"schema_version":"1","source":{"duration_s":10,"has_audio":false},"transcript":{"engine":"none","segments":[]},"screen_keywords":[],"gaps":["无音轨"]}`)
	m, err := parseMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasAudio || m.Engine != "none" || m.Segments != 0 || m.Gaps != 1 {
		t.Errorf("meta = %+v", m)
	}
}

func TestHTTPSummarizerHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","llm":false}`))
	}))
	defer ok.Close()
	if err := NewHTTPSummarizer(ok.URL, 0, "").Health(context.Background()); err != nil {
		t.Errorf("healthy service: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if err := NewHTTPSummarizer(bad.URL, 0, "").Health(context.Background()); err == nil {
		t.Errorf("expected health error on 503")
	}

	if err := NewHTTPSummarizer("http://127.0.0.1:9", 0, "").Health(context.Background()); err == nil {
		t.Errorf("expected error on unreachable host")
	}
}
