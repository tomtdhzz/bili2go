package summarizer

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// MaxBody 是 /summarize 请求体上限（长视频 digest.json 可达数 MB）。
const MaxBody = 64 << 20 // 64 MiB

// Handler 是摘要服务的 HTTP 边界：GET /health、POST /summarize。
// 无状态：每请求读环境变量（对齐 Python app.py，便于测试切换 token/LLM 配置）。
type Handler struct{}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/health":
		llm := os.Getenv("OPENAI_BASE_URL") != "" && os.Getenv("OPENAI_API_KEY") != ""
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "llm": llm})
	case r.Method == http.MethodPost && path == "/summarize":
		h.handleSummarize(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (h Handler) handleSummarize(w http.ResponseWriter, r *http.Request) {
	if !authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="summarizer"`)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	top := defaultTop()
	if q := r.URL.Query().Get("top"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "top 必须为整数"})
			return
		}
		top = n
	}
	if r.ContentLength <= 0 || r.ContentLength > MaxBody {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("Content-Length 非法或超限: %d", r.ContentLength)})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("读取请求体失败: %v", err)})
		return
	}
	var digest map[string]any
	if err := json.Unmarshal(raw, &digest); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("digest 非合法 JSON: %v", err)})
		return
	}
	res, err := Summarize(r.Context(), digest, top)
	if err != nil {
		// 校验失败（ErrInvalid）→ 400，透传 Python 口径的错误串。
		if errors.Is(err, ErrInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	resp := map[string]any{"markdown": res.Markdown, "model": res.Model, "fallback": res.Fallback}
	if res.Reason != "" {
		resp["reason"] = res.Reason
	}
	writeJSON(w, http.StatusOK, resp)
}

// authorized：SUMMARIZER_TOKEN 未设 → 放行（鉴权 opt-in）；已设则要 Bearer 且恒定时间比较。
func authorized(r *http.Request) bool {
	token := strings.TrimSpace(os.Getenv("SUMMARIZER_TOKEN"))
	if token == "" {
		return true
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	presented := strings.TrimSpace(h[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
}

func defaultTop() int {
	if v := os.Getenv("SUMMARY_TOP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 25
}

// writeJSON 以 UTF-8 写 JSON，不转义 HTML（对齐 Python ensure_ascii=False，保 markdown 原样）。
func writeJSON(w http.ResponseWriter, code int, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(obj)
	body := strings.TrimRight(buf.String(), "\n") // Encoder 追加换行，去掉以对齐 Content-Length
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}
