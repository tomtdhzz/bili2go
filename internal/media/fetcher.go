package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"bili2go/internal/app"
	"bili2go/internal/bilibili"
)

// HTTPFetcher 通过 bilibili.Client 下载单条流（主源→备源顺序回退）。
// 复用 Client.NewRequest 注入的 Referer/UA/Cookie —— CDN 必需，否则 403。
type HTTPFetcher struct {
	c *bilibili.Client
}

var _ app.Fetcher = (*HTTPFetcher)(nil)

// NewHTTPFetcher 构造下载器。
func NewHTTPFetcher(c *bilibili.Client) *HTTPFetcher {
	return &HTTPFetcher{c: c}
}

// Fetch 按 urls 顺序尝试下载到 dst：首个返回 200/206 的响应流式写入 dst 后返回；
// 全部失败返回最后一个错误。
func (f *HTTPFetcher) Fetch(ctx context.Context, urls []string, dst string) error {
	if len(urls) == 0 {
		return fmt.Errorf("media: no urls to fetch")
	}
	var lastErr error
	for _, u := range urls {
		if err := f.fetchOne(ctx, u, dst); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("media: all sources failed: %w", lastErr)
}

func (f *HTTPFetcher) fetchOne(ctx context.Context, u, dst string) error {
	req, err := f.c.NewRequest(ctx, http.MethodGet, u)
	if err != nil {
		return fmt.Errorf("media: build request %q: %w", u, err)
	}
	resp, err := f.c.Do(req)
	if err != nil {
		return fmt.Errorf("media: do request %q: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("media: unexpected status %d from %q", resp.StatusCode, u)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("media: create %q: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("media: write %q: %w", dst, err)
	}
	return nil
}
