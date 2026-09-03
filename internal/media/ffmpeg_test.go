package media

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestMux_PropagatesFFmpegError(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available in PATH")
	}
	m := NewFFmpegMuxer()
	err := m.Mux(context.Background(), "/nonexistent/video.m4s", "/nonexistent/audio.m4s", "/tmp/bili2go-mux-out.mp4")
	if err == nil {
		t.Fatalf("expected error for nonexistent inputs, got nil")
	}
	// ffmpeg 会把诊断写入 stderr，错误应包含其片段。
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("error should mention ffmpeg, got %q", err.Error())
	}
}

func TestMux_MissingBinary(t *testing.T) {
	m := &FFmpegMuxer{bin: "definitely-not-a-real-binary-xyz"}
	if err := m.Mux(context.Background(), "a", "b", "c"); err == nil {
		t.Fatalf("expected error for missing binary, got nil")
	}
}
