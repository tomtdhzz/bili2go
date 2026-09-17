package analyze

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// screenKeywordJSON 是 digest.json 里 screen_keywords[] 的一项（与摘要消费方 schema 一致）。
type screenKeywordJSON struct {
	Term           string  `json:"term"`
	FirstTS        float64 `json:"first_t_s"`
	LastTS         float64 `json:"last_t_s"`
	Occurrences    int     `json:"occurrences"`
	Score          float64 `json:"score"`
	Spoken         bool    `json:"spoken_in_transcript"`
	IsChrome       bool    `json:"is_chrome"`
	EvidenceFrames []int   `json:"evidence_frames"`
}

// frameOCR 是一帧的 OCR 结果：帧序号、时间戳、识别出的文本行。
type frameOCR struct {
	Index int
	TS    float64
	Lines []string
}

func joinSegText(segs []digestSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// ocr 抽帧 + tesseract 识别 + 聚合出画面关键字。tesseract 不在 PATH 时优雅跳过（返回空）。
func (d *LocalDigester) ocr(ctx context.Context, videoPath, outDir string, dur float64, transcriptText string) ([]screenKeywordJSON, error) {
	if _, err := exec.LookPath(d.TesseractBin); err != nil {
		fmt.Fprintf(os.Stderr, "localdigest: 未找到 %s，跳过画面 OCR（screen_keywords 留空）\n", d.TesseractBin)
		return nil, nil
	}
	// 抽帧间隔：按时长与 MaxFrames 约束，长视频稀疏采样。
	interval := 2.0
	if dur > 0 && d.MaxFrames > 0 {
		if want := dur / float64(d.MaxFrames); want > interval {
			interval = want
		}
	}
	framesDir := filepath.Join(outDir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(framesDir) // 证据帧只在 summary 里以编号引用，jpg 本体无需保留
	pattern := filepath.Join(framesDir, "f%05d.jpg")
	if _, err := output(ctx, d.FFmpegBin, "-y", "-i", videoPath,
		"-vf", fmt.Sprintf("fps=1/%.4f", interval), "-q:v", "3", pattern); err != nil {
		return nil, fmt.Errorf("ffmpeg 抽帧: %w", err)
	}
	files, _ := filepath.Glob(filepath.Join(framesDir, "f*.jpg"))
	sort.Strings(files)
	if len(files) > d.MaxFrames {
		files = files[:d.MaxFrames]
	}

	frames := make([]frameOCR, 0, len(files))
	for i, f := range files {
		out, err := output(ctx, d.TesseractBin, f, "stdout", "-l", d.OCRLang)
		if err != nil {
			continue // 单帧 OCR 失败不致命
		}
		frames = append(frames, frameOCR{
			Index: i + 1,
			TS:    float64(i) * interval,
			Lines: strings.Split(string(out), "\n"),
		})
	}
	mode := d.Keywords
	if mode == "" {
		mode = "unspoken"
	}
	return aggregateKeywords(frames, transcriptText, mode), nil
}

// aggregateKeywords 把逐帧 OCR 文本聚合成画面关键字（纯函数，可单测）。
//   - term 去重、跨帧计数，first/last 时间戳、证据帧
//   - is_chrome：跨大多数帧常驻（界面装饰/水印）
//   - spoken_in_transcript：归一化后出现在转写文本里
//   - mode=="unspoken" 时丢弃已口述项；score 降序
func aggregateKeywords(frames []frameOCR, transcriptText, mode string) []screenKeywordJSON {
	numFrames := len(frames)
	type agg struct {
		firstTS, lastTS float64
		frames          []int
	}
	m := map[string]*agg{}
	order := []string{}
	for _, f := range frames {
		seen := map[string]bool{}
		for _, line := range f.Lines {
			term := normalizeTerm(line)
			if term == "" || seen[term] {
				continue
			}
			seen[term] = true
			a := m[term]
			if a == nil {
				a = &agg{firstTS: f.TS, lastTS: f.TS}
				m[term] = a
				order = append(order, term)
			}
			a.lastTS = f.TS
			a.frames = append(a.frames, f.Index)
		}
	}

	normTranscript := normalizeForMatch(transcriptText)
	out := []screenKeywordJSON{}
	for _, term := range order {
		a := m[term]
		occ := len(a.frames)
		spoken := normTranscript != "" && strings.Contains(normTranscript, normalizeForMatch(term))
		if mode == "unspoken" && spoken {
			continue
		}
		isChrome := numFrames >= 4 && float64(occ)/float64(numFrames) >= 0.6
		ev := a.frames
		if len(ev) > 3 {
			ev = ev[:3]
		}
		out = append(out, screenKeywordJSON{
			Term:           term,
			FirstTS:        a.firstTS,
			LastTS:         a.lastTS,
			Occurrences:    occ,
			Score:          scoreTerm(term, occ, numFrames),
			Spoken:         spoken,
			IsChrome:       isChrome,
			EvidenceFrames: ev,
		})
	}
	// score 降序（摘要 ③ 表也按此排；此处预排便于阅读与稳定）
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// normalizeTerm 归一化一行 OCR 文本为候选关键字；无效（过短/纯符号）返回空。
func normalizeTerm(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) < 2 || !hasAlnumCJK(s) {
		return ""
	}
	return s
}

// normalizeForMatch 去掉空白与标点、转小写，用于 spoken 子串判定。
func normalizeForMatch(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func hasAlnumCJK(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func hasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// scoreTerm 画面关键字显著性启发式：长度（信息密度）+ 含数字（数据）+ 罕见（非常驻）。
func scoreTerm(term string, occ, numFrames int) float64 {
	n := utf8.RuneCountInString(term)
	score := 1.0 + clampF(float64(n)/12.0, 0.2, 2.0)
	if hasDigit(term) {
		score += 0.5
	}
	if numFrames > 0 {
		score += (1.0 - float64(occ)/float64(numFrames)) * 0.5
	}
	return float64(int(score*1000+0.5)) / 1000
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
