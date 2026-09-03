package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	defaultUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	defaultReferer = "https://www.bilibili.com"
)

// Client 封装对 api.bilibili.com 与 CDN 的 HTTP 访问，统一注入 Referer/UA/Cookie。
type Client struct {
	HTTP     *http.Client
	UA       string
	Referer  string
	SESSDATA string
}

// NewClient 构造客户端；sessdata 可空（决定可获取的清晰度上限）。
func NewClient(sessdata string) *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		UA:       defaultUA,
		Referer:  defaultReferer,
		SESSDATA: sessdata,
	}
}

// NewRequest 构造带统一请求头的请求。
func (c *Client) NewRequest(ctx context.Context, method, rawurl string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Referer", c.Referer)
	if c.SESSDATA != "" {
		req.Header.Set("Cookie", "SESSDATA="+c.SESSDATA)
	}
	return req, nil
}

// Do 执行请求。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.HTTP.Do(req)
}

// APIError 上游业务错误（code != 0）。
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bilibili api error %d: %s", e.Code, e.Message)
}

// apiEnvelope B 站通用响应信封。
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// GetJSON GET 一个 api.bilibili.com JSON 接口，校验 code==0 后把 data 反序列化到 out。
func (c *Client) GetJSON(ctx context.Context, rawurl string, out any) error {
	req, err := c.NewRequest(ctx, http.MethodGet, rawurl)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if env.Code != 0 {
		return &APIError{Code: env.Code, Message: env.Message}
	}
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode data: %w", err)
		}
	}
	return nil
}

// BuildURL 拼接 base 与查询参数（供适配器构造 view/playurl 地址）。
func BuildURL(base string, q url.Values) string {
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}
