package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExecDigester 通过 exec 调用宿主上的 video-digest 二进制（macOS 原生，不进 docker）。
//
// video-digest 退出码：0 成功（可能带 gaps）；2 输入不可读/无音视频轨；3 环境或模型资产不可用。
type ExecDigester struct {
	Bin      string // video-digest 可执行文件路径（默认 "video-digest"，走 PATH）
	Lang     string // 转写 locale，默认 zh-CN
	Keywords string // unspoken | all，默认 unspoken
	// ExtraArgs 追加到命令末尾（如 --sample-interval 0.5），可空。
	ExtraArgs []string
}

// NewExecDigester 用默认参数构造。bin 为空时用 "video-digest"。
func NewExecDigester(bin string) *ExecDigester {
	if bin == "" {
		bin = "video-digest"
	}
	return &ExecDigester{Bin: bin, Lang: "zh-CN", Keywords: "unspoken"}
}

// Digest 对 videoPath 运行 video-digest，产物写入 outDir，读回 digest.json。
func (e *ExecDigester) Digest(ctx context.Context, videoPath, outDir string) (DigestOutput, error) {
	if _, err := os.Stat(videoPath); err != nil {
		return DigestOutput{}, fmt.Errorf("video not found: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return DigestOutput{}, err
	}
	lang := e.Lang
	if lang == "" {
		lang = "zh-CN"
	}
	kw := e.Keywords
	if kw == "" {
		kw = "unspoken"
	}
	args := []string{videoPath, "--out", outDir, "--lang", lang, "--keywords", kw}
	args = append(args, e.ExtraArgs...)

	cmd := exec.CommandContext(ctx, e.Bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return DigestOutput{}, fmt.Errorf("video-digest %v: %w: %s", args, err, strings.TrimSpace(stderr.String()))
	}

	digestPath := filepath.Join(outDir, "digest.json")
	raw, err := os.ReadFile(digestPath)
	if err != nil {
		return DigestOutput{}, fmt.Errorf("read digest.json: %w", err)
	}
	meta, err := parseMeta(raw)
	if err != nil {
		return DigestOutput{}, err
	}
	transcriptPath := filepath.Join(outDir, "transcript.md")
	if _, err := os.Stat(transcriptPath); err != nil {
		transcriptPath = "" // --json-only 或未产出
	}
	return DigestOutput{
		Dir:            outDir,
		DigestPath:     digestPath,
		TranscriptPath: transcriptPath,
		JSON:           raw,
		Meta:           meta,
	}, nil
}

// parseMeta 从 digest.json 原始字节里取展示/校验需要的最小字段。
func parseMeta(raw []byte) (DigestMeta, error) {
	var d struct {
		SchemaVersion string `json:"schema_version"`
		Source        struct {
			DurationS float64 `json:"duration_s"`
			HasAudio  bool    `json:"has_audio"`
		} `json:"source"`
		Transcript struct {
			Engine   string            `json:"engine"`
			Segments []json.RawMessage `json:"segments"`
		} `json:"transcript"`
		ScreenKeywords []json.RawMessage `json:"screen_keywords"`
		Gaps           []json.RawMessage `json:"gaps"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return DigestMeta{}, fmt.Errorf("parse digest.json: %w", err)
	}
	return DigestMeta{
		SchemaVersion: d.SchemaVersion,
		DurationS:     d.Source.DurationS,
		HasAudio:      d.Source.HasAudio,
		Engine:        d.Transcript.Engine,
		Segments:      len(d.Transcript.Segments),
		Keywords:      len(d.ScreenKeywords),
		Gaps:          len(d.Gaps),
	}, nil
}
