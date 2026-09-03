package media

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"bili2go/internal/app"
)

// FFmpegMuxer 通过外部 ffmpeg 将 video 与 audio 无损封装为 dst。
type FFmpegMuxer struct {
	bin string
}

var _ app.Muxer = (*FFmpegMuxer)(nil)

// NewFFmpegMuxer 构造 Muxer，默认使用 PATH 中的 "ffmpeg"。
func NewFFmpegMuxer() *FFmpegMuxer {
	return &FFmpegMuxer{bin: "ffmpeg"}
}

// Mux 执行 ffmpeg -y -i video -i audio -c copy dst；非零退出返回含 stderr 的错误。
func (m *FFmpegMuxer) Mux(ctx context.Context, videoPath, audioPath, dst string) error {
	cmd := exec.CommandContext(ctx, m.bin, "-y", "-i", videoPath, "-i", audioPath, "-c", "copy", dst)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("media: ffmpeg mux failed: %w: %s", err, stderr.String())
	}
	return nil
}
