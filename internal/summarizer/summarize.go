// Package summarizer 把 video-digest 的 digest.json 转成五节 summary.md。
//
// 两条路径（与既有 Python 实现逐字对齐）：
//   - LLM：配置 OPENAI_BASE_URL + OPENAI_API_KEY 时，把 digest 压成简报喂 OpenAI 兼容的
//     /v1/chat/completions，产出自然语言五节摘要。
//   - 回退：未配置或调用失败时，用确定性模板直接从 digest 字段拼装五节结构（全部挂时间码，
//     不编造结论），保证离线可跑、可验收。
//
// 仅依赖 Go 标准库。digest 用 map[string]any 动态解析，对齐 Python .get() 语义、避免 schema 漂移。
package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrInvalid 标记 digest 校验失败（缺必需字段）。HTTP 层据此返回 400。
var ErrInvalid = errors.New("invalid digest")

type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

// Result 是一次摘要的产物：markdown 正文 + 模型 + 是否回退 + 回退原因。
type Result struct {
	Markdown string
	Model    string
	Fallback bool
	Reason   string
}

var requiredKeys = []string{"source", "transcript", "screen_keywords", "gaps"}

const systemPrompt = `你是视频摘要助手。输入是 video-digest 的确定性简报（口述转写分段 + 画面未口述关键字 + 缺口）。
产出固定五节的 Markdown，节标题必须逐字为：
## ① 一句话结论
## ② 分段摘要
## ③ 画面未口述关键字表
## ④ 口述与画面双重印证的要点
## ⑤ 缺口与置信度
纪律（违反视为错误）：
- 每条结论必须引用证据：口述结论引用分段的 [mm:ss]；画面结论引用关键字的首次出现 [mm:ss] 与证据帧。
- 分段摘要每条以 [mm:ss] 开头，时间码直接来自分段 start，不得四舍五入到别的段。
- 无口述数据（转写引擎为 none 或无分段）时，第②④节写「无口述内容」，禁止用画面文字反推「他说了…」。
- 第③节按 score 降序把画面未口述关键字逐条落表（term | 首次出现 | 出现帧数 | 证据帧）。
- 第⑤节把 gaps 原样展开，一条不漏；gaps 为空则明确写「无缺口」，不得改写成「基本没问题」。
- 数字只能来自简报，不得自行估算或编造。只输出 Markdown，不要额外说明。`

// --- 取值助手（对齐 Python 动态 dict / .get()）---

func asMap(v any) map[string]any  { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any         { s, _ := v.([]any); return s }
func getStr(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func getBool(m map[string]any, k string) bool  { b, _ := m[k].(bool); return b }

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func toInt(v any) int { return int(toFloat(v)) }

// clock 复刻 Python clock：int(float(s)) 后 mm:ss 零填充。
func clock(v any) string {
	s := toInt(v)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// pyList 复刻 Python 列表 repr：['a', 'b', 'c']（保 F2 错误串观测口径）。
func pyList(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = "'" + x + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func validate(d map[string]any) error {
	var missing []string
	for _, k := range requiredKeys {
		if _, ok := d[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return &invalidError{fmt.Sprintf("digest 缺少必需字段: %s", pyList(missing))}
	}
	return nil
}

// partition 三分关键字：unspoken / spoken / chrome；unspoken 按 score 稳定降序。
func partition(kws []any) (unspoken, spoken, chrome []map[string]any) {
	for _, it := range kws {
		k := asMap(it)
		switch {
		case getBool(k, "is_chrome"):
			chrome = append(chrome, k)
		case getBool(k, "spoken_in_transcript"):
			spoken = append(spoken, k)
		default:
			unspoken = append(unspoken, k)
		}
	}
	sort.SliceStable(unspoken, func(i, j int) bool {
		return toFloat(unspoken[i]["score"]) > toFloat(unspoken[j]["score"])
	})
	return
}

func firstN[T any](xs []T, n int) []T {
	if n < len(xs) {
		return xs[:n]
	}
	return xs
}

func evidenceFrames(k map[string]any) string {
	var parts []string
	for _, i := range firstN(asSlice(k["evidence_frames"]), 3) {
		parts = append(parts, fmt.Sprintf("frames/f%05d.jpg", toInt(i)))
	}
	return strings.Join(parts, "、")
}

// fallbackSummary 确定性模板：从 digest 字段拼装五节，全部挂时间码，不编造结论。
func fallbackSummary(d map[string]any, top int) string {
	src := asMap(d["source"])
	tr := asMap(d["transcript"])
	segs := asSlice(tr["segments"])
	engine := getStr(tr, "engine")
	hasSpoken := engine != "" && engine != "none" && len(segs) > 0
	unspoken, spoken, chrome := partition(asSlice(d["screen_keywords"]))
	dur := clock(src["duration_s"])
	var out []string

	// ① 一句话结论
	out = append(out, "## ① 一句话结论")
	switch {
	case hasSpoken:
		gist := strings.TrimSpace(getStr(asMap(segs[0]), "text"))
		out = append(out, fmt.Sprintf("全片时长 %s，口述开场：%s（完整脉络见第②节，画面独有信息见第③节）。", dur, gist))
	case len(unspoken) > 0:
		out = append(out, fmt.Sprintf("全片时长 %s，无口述数据；画面关键信息以「%s」（[%s]）为首，详见第③节。",
			dur, getStr(unspoken[0], "term"), clock(unspoken[0]["first_t_s"])))
	default:
		out = append(out, fmt.Sprintf("全片时长 %s，无口述数据、无画面未口述关键字，可总结素材有限。", dur))
	}

	// ② 分段摘要
	out = append(out, "\n## ② 分段摘要")
	if hasSpoken {
		for _, s := range segs {
			sm := asMap(s)
			out = append(out, fmt.Sprintf("- [%s] %s", clock(sm["start_s"]), strings.TrimSpace(getStr(sm, "text"))))
		}
	} else {
		out = append(out, "无音轨，无口述内容。")
	}

	// ③ 画面未口述关键字表
	out = append(out, "\n## ③ 画面未口述关键字表")
	if len(unspoken) > 0 {
		out = append(out, "| term | 首次出现 | 出现帧数 | 证据帧 |")
		out = append(out, "|---|---|---|---|")
		for _, k := range firstN(unspoken, top) {
			ev := evidenceFrames(k)
			if ev == "" {
				ev = "(无)"
			}
			out = append(out, fmt.Sprintf("| %s | [%s] | %d | %s |",
				getStr(k, "term"), clock(k["first_t_s"]), toInt(k["occurrences"]), ev))
		}
	} else {
		out = append(out, "（无画面未口述关键字：画面文字或已被口述，或被判为界面装饰。）")
	}

	// ④ 口述与画面双重印证的要点
	out = append(out, "\n## ④ 口述与画面双重印证的要点")
	switch {
	case !hasSpoken:
		out = append(out, "无口述数据，无法双重印证。")
	case len(spoken) > 0:
		for _, k := range firstN(spoken, top) {
			out = append(out, fmt.Sprintf("- [%s] %s（口述与画面均出现，证据帧 %d 帧）",
				clock(k["first_t_s"]), getStr(k, "term"), toInt(k["occurrences"])))
		}
	default:
		out = append(out, "本次以 `--keywords unspoken` 生成，已口述项未收录；如需本节请用 `--keywords all` 重跑。")
	}

	// ⑤ 缺口与置信度
	out = append(out, "\n## ⑤ 缺口与置信度")
	gaps := asSlice(d["gaps"])
	if len(gaps) > 0 {
		for _, g := range gaps {
			s, _ := g.(string)
			out = append(out, fmt.Sprintf("- %s", s))
		}
	} else {
		out = append(out, "本次运行无缺口（gaps 为空）。")
	}
	if len(chrome) > 0 {
		terms := make([]string, 0, len(chrome))
		for _, k := range firstN(chrome, top) {
			terms = append(terms, getStr(k, "term"))
		}
		out = append(out, fmt.Sprintf("\n> 另有 %d 条常驻文本/界面装饰已按 is_chrome 排除，不进结论：%s",
			len(chrome), strings.Join(terms, "、")))
	}
	return strings.Join(out, "\n") + "\n"
}

// buildBrief 把 digest 压成 LLM 可读简报（仅 LLM 路径用）。
func buildBrief(d map[string]any, top int) string {
	src := asMap(d["source"])
	run := asMap(d["run"])
	tr := asMap(d["transcript"])
	unspoken, spoken, chrome := partition(asSlice(d["screen_keywords"]))
	mode := "?"
	if p := asMap(run["params"]); p != nil {
		if m, ok := p["keywords"].(string); ok {
			mode = m
		}
	}
	path := getStr(src, "path")
	if path == "" {
		path = "(unknown)"
	}
	hasAudio := "无"
	if getBool(src, "has_audio") {
		hasAudio = "有"
	}
	lines := []string{
		fmt.Sprintf("# %s", path),
		fmt.Sprintf("时长 %s · %sx%s · 音轨 %s · 关键帧 %d · keywords 模式 %s",
			clock(src["duration_s"]), numStr(src["width"]), numStr(src["height"]), hasAudio, len(asSlice(d["frames"])), mode),
		fmt.Sprintf("转写 %s/%s · %d 段", getStr(tr, "engine"), getStr(tr, "locale"), len(asSlice(tr["segments"]))),
		fmt.Sprintf("gaps: %s", briefGaps(asSlice(d["gaps"]))),
		"",
		"## 转写分段",
	}
	segs := asSlice(tr["segments"])
	for _, s := range segs {
		sm := asMap(s)
		lines = append(lines, fmt.Sprintf("[%s] %s", clock(sm["start_s"]), getStr(sm, "text")))
	}
	if len(segs) == 0 {
		lines = append(lines, "(无分段)")
	}
	lines = append(lines, fmt.Sprintf("\n## 画面未口述关键字（%d 条，取前 %d）", len(unspoken), min(top, len(unspoken))))
	for _, k := range firstN(unspoken, top) {
		var ev []string
		for _, i := range firstN(asSlice(k["evidence_frames"]), 3) {
			ev = append(ev, fmt.Sprintf("frames/f%05d.jpg", toInt(i)))
		}
		lines = append(lines, fmt.Sprintf("[%s] %s  score=%s frames=%s ev=[%s]",
			clock(k["first_t_s"]), getStr(k, "term"), numStr(k["score"]), numStr(k["occurrences"]),
			strings.Join(quoteAll(ev), ", ")))
	}
	if len(spoken) > 0 {
		lines = append(lines, fmt.Sprintf("\n## 口述+画面双重印证（%d 条）", len(spoken)))
		for _, k := range firstN(spoken, top) {
			lines = append(lines, fmt.Sprintf("[%s] %s  frames=%s", clock(k["first_t_s"]), getStr(k, "term"), numStr(k["occurrences"])))
		}
	}
	if len(chrome) > 0 {
		lines = append(lines, "\n## 已排除的常驻文本（不进结论）")
		terms := make([]string, 0, len(chrome))
		for _, k := range firstN(chrome, top) {
			terms = append(terms, getStr(k, "term"))
		}
		lines = append(lines, strings.Join(terms, "、"))
	}
	return strings.Join(lines, "\n")
}

func briefGaps(gaps []any) string {
	if len(gaps) == 0 {
		return "(无)"
	}
	parts := make([]string, len(gaps))
	for i, g := range gaps {
		s, _ := g.(string)
		parts[i] = "'" + s + "'"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "'" + x + "'"
	}
	return out
}

// numStr 把 JSON 数字按 Python str() 口径打印：整值去小数，非整值保留。
func numStr(v any) string {
	f := toFloat(v)
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func callLLM(ctx context.Context, brief, baseURL, apiKey, model string) (string, error) {
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	payload := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": brief},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("llm status %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm response has no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content) + "\n", nil
}

// Summarize 返回摘要结果。digest 校验失败返回 ErrInvalid。
func Summarize(ctx context.Context, d map[string]any, top int) (Result, error) {
	if err := validate(d); err != nil {
		return Result{}, err
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	model := strings.TrimSpace(os.Getenv("OPENAI_MODEL"))
	if model == "" {
		model = "gpt-4o-mini"
	}
	if baseURL != "" && apiKey != "" {
		md, err := callLLM(ctx, buildBrief(d, top), baseURL, apiKey, model)
		if err == nil {
			return Result{Markdown: md, Model: model, Fallback: false}, nil
		}
		return Result{
			Markdown: fallbackSummary(d, top),
			Model:    "fallback",
			Fallback: true,
			Reason:   "LLM 调用失败，已回退确定性模板: " + err.Error(),
		}, nil
	}
	return Result{Markdown: fallbackSummary(d, top), Model: "fallback", Fallback: true}, nil
}
