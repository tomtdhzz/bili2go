package analyze

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestLocalDigesterIntegration 跑真实 ffprobe+ffmpeg+whisper 全链路。
// 默认跳过；需 SMOKE_VIDEO（视频路径）+ WHISPER_MODEL，且 PATH 有 whisper-cli/ffmpeg/ffprobe。
//
//	SMOKE_VIDEO=/tmp/smoke.mp4 WHISPER_MODEL=/tmp/ggml-base.bin \
//	  go test ./internal/analyze -run TestLocalDigesterIntegration -v
func TestLocalDigesterIntegration(t *testing.T) {
	video := os.Getenv("SMOKE_VIDEO")
	if video == "" || os.Getenv("WHISPER_MODEL") == "" {
		t.Skip("set SMOKE_VIDEO + WHISPER_MODEL to run")
	}
	for _, b := range []string{"ffprobe", "ffmpeg", "whisper-cli"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not on PATH", b)
		}
	}
	dir := t.TempDir()
	out, err := NewLocalDigester("", "", "zh").Digest(context.Background(), video, dir)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(out.JSON, &d); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "transcript", "screen_keywords", "gaps"} {
		if _, ok := d[k]; !ok {
			t.Errorf("digest 缺必需键 %q", k)
		}
	}
	if out.Meta.Engine != "whisper" || out.Meta.Segments == 0 {
		t.Errorf("engine=%q segments=%d, want whisper + >0", out.Meta.Engine, out.Meta.Segments)
	}
	t.Logf("digest OK: engine=%s segments=%d dur=%.1fs", out.Meta.Engine, out.Meta.Segments, out.Meta.DurationS)
}

// TestLocalDigesterOCRIntegration 跑真实 ffmpeg 抽帧 + tesseract OCR。
// 默认跳过；需 SMOKE_OCR_VIDEO（一段含屏幕文字的视频）+ PATH 有 tesseract/ffmpeg/ffprobe。
//
//	SMOKE_OCR_VIDEO=/tmp/slide.mp4 go test ./internal/analyze -run TestLocalDigesterOCRIntegration -v
func TestLocalDigesterOCRIntegration(t *testing.T) {
	video := os.Getenv("SMOKE_OCR_VIDEO")
	if video == "" {
		t.Skip("set SMOKE_OCR_VIDEO (a video with on-screen text) to run")
	}
	for _, b := range []string{"ffprobe", "ffmpeg", "tesseract"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s not on PATH", b)
		}
	}
	d := NewLocalDigester("", "", "zh")
	d.OCRLang = "eng" // 本机仅装了 eng 语言包
	d.Keywords = "unspoken"
	out, err := d.Digest(context.Background(), video, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var dj map[string]any
	if err := json.Unmarshal(out.JSON, &dj); err != nil {
		t.Fatal(err)
	}
	kws, _ := dj["screen_keywords"].([]any)
	if len(kws) == 0 {
		t.Fatalf("expected screen_keywords from OCR, got none")
	}
	found := false
	for _, k := range kws {
		m, _ := k.(map[string]any)
		if term, _ := m["term"].(string); strings.Contains(term, "REVENUE") || strings.Contains(term, "MARGIN") || strings.Contains(term, "240") {
			found = true
		}
	}
	if !found {
		t.Errorf("OCR terms missing expected on-screen text: %v", kws)
	}
	t.Logf("OCR produced %d screen_keywords", len(kws))
}
