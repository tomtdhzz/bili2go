package analyze

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// LocalDigester 是可容器化的 digest 适配器（实现 Digester 端口），与 macOS 专属的 ExecDigester 平行。
//
// 它 exec 三个跨平台二进制产出与 video-digest 同 schema 的 digest.json：
//   - ffprobe：探测时长/分辨率/是否有音轨
//   - ffmpeg： 抽 16kHz 单声道 wav（whisper 输入）
//   - whisper.cpp：转写出带时间戳的分段
//
// 阶段 1 只做口述转写，screen_keywords 留空（摘要服务对空关键字优雅降级）；画面 OCR 关键字为阶段 2。
type LocalDigester struct {
	FFprobeBin   string // 默认 "ffprobe"
	FFmpegBin    string // 默认 "ffmpeg"
	WhisperBin   string // 默认 "whisper-cli"（whisper.cpp 的 CLI）
	WhisperModel string // ggml 模型路径（默认取 env WHISPER_MODEL）
	Lang         string // 转写语言，默认 "zh"
	TesseractBin string // 默认 "tesseract"（画面 OCR）
	Keywords     string // none|unspoken|all，默认 "unspoken"（none=跳过 OCR）
	OCRLang      string // tesseract 语言，默认 "chi_sim+eng"
	MaxFrames    int    // OCR 抽帧上限，默认 60
}

// NewLocalDigester 用默认参数构造；空字段回落到默认二进制名与 env。
func NewLocalDigester(whisperBin, model, lang string) *LocalDigester {
	d := &LocalDigester{
		FFprobeBin:   "ffprobe",
		FFmpegBin:    "ffmpeg",
		WhisperBin:   whisperBin,
		WhisperModel: model,
		Lang:         lang,
	}
	if d.WhisperBin == "" {
		d.WhisperBin = "whisper-cli"
	}
	if d.WhisperModel == "" {
		d.WhisperModel = os.Getenv("WHISPER_MODEL")
	}
	if d.Lang == "" {
		d.Lang = "zh"
	}
	if d.TesseractBin == "" {
		d.TesseractBin = "tesseract"
	}
	if d.Keywords == "" {
		d.Keywords = "unspoken"
	}
	if d.OCRLang == "" {
		d.OCRLang = "chi_sim+eng"
	}
	if d.MaxFrames == 0 {
		d.MaxFrames = 60
	}
	return d
}

// sourceInfo 是 ffprobe 探测出的媒体信息。
type sourceInfo struct {
	DurationS float64
	Width     int
	Height    int
	HasAudio  bool
}

// digestJSON 是产出 digest.json 的强类型形状（本适配器是生产方，schema 固定）。
type digestJSON struct {
	SchemaVersion  string              `json:"schema_version"`
	Source         digestSource        `json:"source"`
	Transcript     digestTr            `json:"transcript"`
	ScreenKeywords []screenKeywordJSON `json:"screen_keywords"`
	Gaps           []string            `json:"gaps"`
	Frames         []any               `json:"frames"`
}

type digestSource struct {
	Path      string  `json:"path"`
	DurationS float64 `json:"duration_s"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	HasAudio  bool    `json:"has_audio"`
}

type digestTr struct {
	Engine   string      `json:"engine"`
	Locale   string      `json:"locale"`
	Segments []digestSeg `json:"segments"`
	Text     string      `json:"text"`
}

type digestSeg struct {
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
	Text   string  `json:"text"`
}

// Digest 对 videoPath 运行 ffprobe→ffmpeg→whisper，产物写入 outDir，返回 DigestOutput。
func (d *LocalDigester) Digest(ctx context.Context, videoPath, outDir string) (DigestOutput, error) {
	if _, err := os.Stat(videoPath); err != nil {
		return DigestOutput{}, fmt.Errorf("video not found: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return DigestOutput{}, err
	}

	src, err := d.probe(ctx, videoPath)
	if err != nil {
		return DigestOutput{}, err
	}

	engine, segs := "none", []digestSeg{}
	if src.HasAudio {
		if s, err := d.transcribe(ctx, videoPath, outDir); err != nil {
			// 转写失败按优雅降级处理：engine=none，摘要仍能产出（RS5）。
			fmt.Fprintf(os.Stderr, "localdigest: whisper 失败，按无口述降级: %v\n", err)
		} else if len(s) > 0 {
			engine, segs = "whisper", s
		}
	}

	keywords := []screenKeywordJSON{}
	if d.Keywords != "none" {
		if kw, oerr := d.ocr(ctx, videoPath, outDir, src.DurationS, joinSegText(segs)); oerr != nil {
			fmt.Fprintf(os.Stderr, "localdigest: OCR 失败，按无画面关键字降级: %v\n", oerr)
		} else {
			keywords = kw
		}
	}

	dj := assembleDigest(videoPath, src, engine, d.Lang, segs, keywords)
	raw, err := json.MarshalIndent(dj, "", "  ")
	if err != nil {
		return DigestOutput{}, err
	}
	digestPath := filepath.Join(outDir, "digest.json")
	if err := os.WriteFile(digestPath, raw, 0o644); err != nil {
		return DigestOutput{}, err
	}
	meta, err := parseMeta(raw)
	if err != nil {
		return DigestOutput{}, err
	}
	return DigestOutput{
		Dir:        outDir,
		DigestPath: digestPath,
		JSON:       raw,
		Meta:       meta,
	}, nil
}

// probe 用 ffprobe 探测时长/分辨率/音轨。
func (d *LocalDigester) probe(ctx context.Context, videoPath string) (sourceInfo, error) {
	args := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", videoPath}
	out, err := output(ctx, d.FFprobeBin, args...)
	if err != nil {
		return sourceInfo{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

// transcribe 抽音频并跑 whisper，返回分段。
func (d *LocalDigester) transcribe(ctx context.Context, videoPath, outDir string) ([]digestSeg, error) {
	if d.WhisperModel == "" {
		return nil, fmt.Errorf("WHISPER_MODEL 未设置")
	}
	wav := filepath.Join(outDir, "audio.wav")
	defer os.Remove(wav)
	// 16kHz 单声道 wav：whisper.cpp 要求的输入格式。
	if _, err := output(ctx, d.FFmpegBin, "-y", "-i", videoPath, "-vn", "-ac", "1", "-ar", "16000", "-f", "wav", wav); err != nil {
		return nil, fmt.Errorf("ffmpeg 抽音频: %w", err)
	}
	base := filepath.Join(outDir, "whisper")
	// -pp 打印进度；whisper 的 stderr 实时透传到本进程，长视频转写可见推进。
	fmt.Fprintf(os.Stderr, "localdigest: 转写中（whisper 模型 %s，长视频耗时较久）…\n", filepath.Base(d.WhisperModel))
	args := []string{"-m", d.WhisperModel, "-f", wav, "-l", d.Lang, "-oj", "-of", base, "-pp"}
	cmd := exec.CommandContext(ctx, d.WhisperBin, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("whisper: %w", err)
	}
	raw, err := os.ReadFile(base + ".json")
	if err != nil {
		return nil, fmt.Errorf("read whisper json: %w", err)
	}
	return parseWhisperJSON(raw)
}

// --- 纯函数（可单测，不 exec）---

type probeOut struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

// parseProbe 从 ffprobe -print_format json 输出提取 sourceInfo。
func parseProbe(raw []byte) (sourceInfo, error) {
	var p probeOut
	if err := json.Unmarshal(raw, &p); err != nil {
		return sourceInfo{}, fmt.Errorf("parse ffprobe json: %w", err)
	}
	var s sourceInfo
	s.DurationS, _ = strconv.ParseFloat(strings.TrimSpace(p.Format.Duration), 64)
	for _, st := range p.Streams {
		switch st.CodecType {
		case "audio":
			s.HasAudio = true
		case "video":
			if s.Width == 0 {
				s.Width, s.Height = st.Width, st.Height
			}
		}
	}
	return s, nil
}

type whisperOut struct {
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"` // ms
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// parseWhisperJSON 把 whisper.cpp -oj 输出映射为分段（跳过空白段）。
func parseWhisperJSON(raw []byte) ([]digestSeg, error) {
	var w whisperOut
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("parse whisper json: %w", err)
	}
	segs := make([]digestSeg, 0, len(w.Transcription))
	for _, t := range w.Transcription {
		text := strings.TrimSpace(t.Text)
		if text == "" || text == "[BLANK_AUDIO]" {
			continue
		}
		segs = append(segs, digestSeg{
			StartS: float64(t.Offsets.From) / 1000,
			EndS:   float64(t.Offsets.To) / 1000,
			Text:   text,
		})
	}
	return segs, nil
}

// assembleDigest 组装与摘要消费方逐字兼容的 digest.json 结构。
func assembleDigest(videoPath string, src sourceInfo, engine, locale string, segs []digestSeg, keywords []screenKeywordJSON) digestJSON {
	if segs == nil {
		segs = []digestSeg{}
	}
	if keywords == nil {
		keywords = []screenKeywordJSON{}
	}
	texts := make([]string, 0, len(segs))
	for _, s := range segs {
		texts = append(texts, s.Text)
	}
	return digestJSON{
		SchemaVersion: "1",
		Source: digestSource{
			Path:      videoPath,
			DurationS: src.DurationS,
			Width:     src.Width,
			Height:    src.Height,
			HasAudio:  src.HasAudio,
		},
		Transcript: digestTr{
			Engine:   engine,
			Locale:   locale,
			Segments: segs,
			Text:     strings.Join(texts, ""),
		},
		ScreenKeywords: keywords,
		Gaps:           []string{},
		Frames:         []any{},
	}
}

// output 跑一个命令并返回 stdout，stderr 并入错误。
func output(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
