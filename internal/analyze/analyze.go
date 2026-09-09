// Package analyze 编排「下载后分析」链路：视频 → video-digest（宿主/macOS 原生）
// → 摘要服务（docker/HTTP）→ summary.md。
//
// 本包遵循领域中心 + 端口适配器：Analyzer 只依赖 Digester/Summarizer 两个端口，
// 具体的 exec 调用与 HTTP 调用由适配器（digest.go / summarizer.go）实现。分析链路
// 属外围能力，不侵入核心下载领域（domain/app）。
package analyze

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// DigestOutput 是 video-digest 的产物：原始 digest.json 字节 + 解析出的元信息 + 落盘路径。
type DigestOutput struct {
	Dir            string     // 产物目录 <stem>.digest/
	DigestPath     string     // digest.json 绝对路径
	TranscriptPath string     // transcript.md 路径（--json-only 时为空）
	JSON           []byte     // digest.json 原始字节（原样转发给摘要服务，不重序列化）
	Meta           DigestMeta // 解析出的部分字段（供 Report 与 CLI 展示）
}

// DigestMeta 是 digest.json 的最小解析视图（Go 侧只取展示与校验需要的字段）。
type DigestMeta struct {
	SchemaVersion string
	DurationS     float64
	HasAudio      bool
	Engine        string // transcript.engine；"none" 表示无口述
	Segments      int
	Keywords      int // screen_keywords 条目数
	Gaps          int
}

// Summary 是摘要服务的返回：markdown 正文 + 使用的模型 + 是否走了回退模板。
type Summary struct {
	Markdown string
	Model    string
	Fallback bool
	Reason   string // 回退原因（可空）
}

// Digester 端口：对视频运行确定性分析，产出 digest.json。由 ExecDigester（宿主 video-digest）实现。
type Digester interface {
	Digest(ctx context.Context, videoPath, outDir string) (DigestOutput, error)
}

// Summarizer 端口：把 digest.json 变成 summary.md 正文。由 HTTPSummarizer（docker 服务）实现。
type Summarizer interface {
	Summarize(ctx context.Context, digestJSON []byte) (Summary, error)
}

// Report 是一次分析的完整结果与产物路径。
type Report struct {
	VideoPath   string
	Digest      DigestOutput
	Summary     Summary
	SummaryPath string
}

// Analyzer 应用层用例：digest → summarize → 写 summary.md。
type Analyzer struct {
	digester   Digester
	summarizer Summarizer
}

// NewAnalyzer 构造。
func NewAnalyzer(d Digester, s Summarizer) *Analyzer {
	return &Analyzer{digester: d, summarizer: s}
}

// Analyze 对 videoPath 运行完整分析链路，把 summary.md 写入 outDir，返回报告。
// outDir 为空时用 <video 同目录>/<stem>.digest/。
func (a *Analyzer) Analyze(ctx context.Context, videoPath, outDir string) (Report, error) {
	if outDir == "" {
		outDir = DefaultDigestDir(videoPath)
	}
	dg, err := a.digester.Digest(ctx, videoPath, outDir)
	if err != nil {
		return Report{}, fmt.Errorf("digest: %w", err)
	}
	sum, err := a.summarizer.Summarize(ctx, dg.JSON)
	if err != nil {
		return Report{}, fmt.Errorf("summarize: %w", err)
	}
	summaryPath := filepath.Join(dg.Dir, "summary.md")
	if err := os.WriteFile(summaryPath, []byte(sum.Markdown), 0o644); err != nil {
		return Report{}, fmt.Errorf("write summary.md: %w", err)
	}
	return Report{
		VideoPath:   videoPath,
		Digest:      dg,
		Summary:     sum,
		SummaryPath: summaryPath,
	}, nil
}

// DefaultDigestDir 复刻 video-digest 的默认产物目录口径：<video 同目录>/<stem>.digest/。
func DefaultDigestDir(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := filepath.Base(videoPath)
	stem := base[:len(base)-len(filepath.Ext(base))]
	return filepath.Join(dir, stem+".digest")
}
