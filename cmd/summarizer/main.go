// Command summarizer 是 bili2go 分析链路的 LLM/回退摘要服务：digest.json -> summary.md 的 HTTP 边界。
//
//	summarizer          # 启动服务，监听 :$PORT（默认 8091）
//	summarizer health   # 探活自身 /health，200 → exit 0，否则 exit 1（供容器 HEALTHCHECK）
//
// 契约见 docs/tech-design/tech-design-summarizer-go.md。纯 Go 标准库。
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"bili2go/internal/summarizer"
)

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8091"
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(health())
	}
	addr := ":" + port()
	srv := &http.Server{
		Addr:              addr,
		Handler:           summarizer.Handler{},
		ReadHeaderTimeout: 10 * time.Second,
	}
	llm := os.Getenv("OPENAI_BASE_URL") != "" && os.Getenv("OPENAI_API_KEY") != ""
	fmt.Printf("summarizer listening on %s (llm=%v)\n", addr, llm)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func health() int {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port() + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}
