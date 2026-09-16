package analyze

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// HTTPSummarizer 调用 docker 摘要服务（POST /summarize），把 digest.json 变成 summary.md。
//
// 契约：POST <BaseURL>/summarize?top=N，body 为 digest.json；
// 200 返回 {"markdown","model","fallback"[,"reason"]}；非 2xx 返回 {"error"} 视为失败。
type HTTPSummarizer struct {
	BaseURL string       // 如 http://127.0.0.1:8091
	Top     int          // 关键字取前 N；<=0 时不传，用服务默认
	Token   string       // 可空；非空时 /summarize 带 Authorization: Bearer <token>
	Client  *http.Client // 可空，默认 120s 超时
}

// NewHTTPSummarizer 构造。baseURL 为空时用 http://127.0.0.1:8091；token 为空则不鉴权。
func NewHTTPSummarizer(baseURL string, top int, token string) *HTTPSummarizer {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8091"
	}
	return &HTTPSummarizer{
		BaseURL: baseURL,
		Top:     top,
		Token:   token,
		Client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Summarize 把 digestJSON 原样 POST 给摘要服务，解析出 Summary。
func (h *HTTPSummarizer) Summarize(ctx context.Context, digestJSON []byte) (Summary, error) {
	url := h.BaseURL + "/summarize"
	if h.Top > 0 {
		url += "?top=" + strconv.Itoa(h.Top)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(digestJSON))
	if err != nil {
		return Summary{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}

	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Summary{}, fmt.Errorf("call summarizer %s: %w", h.BaseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Summary{}, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = string(body)
		}
		return Summary{}, fmt.Errorf("summarizer %d: %s", resp.StatusCode, e.Error)
	}
	var out struct {
		Markdown string `json:"markdown"`
		Model    string `json:"model"`
		Fallback bool   `json:"fallback"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Summary{}, fmt.Errorf("decode summarizer response: %w", err)
	}
	if out.Markdown == "" {
		return Summary{}, fmt.Errorf("summarizer returned empty markdown")
	}
	return Summary{Markdown: out.Markdown, Model: out.Model, Fallback: out.Fallback, Reason: out.Reason}, nil
}

// Health 探活：GET <BaseURL>/health，2xx 视为可用。用于在昂贵的下载+digest 之前做前置检查。
func (h *HTTPSummarizer) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("summarizer %s unreachable: %w", h.BaseURL, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("summarizer %s health %d", h.BaseURL, resp.StatusCode)
	}
	return nil
}
