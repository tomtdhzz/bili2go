package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"bili2go/internal/domain"
)

// Request 一次下载请求。
type Request struct {
	BVID          string
	Page          int // 1-based；<=0 视为第 1P
	QN            int // 期望清晰度；<=0 取可用最高
	PreferCodec   domain.Codec
	FallbackCodec domain.Codec
}

// Info 解析结果（供 /api/info 与下载复用）。
type Info struct {
	Video    domain.Video
	Page     domain.Page
	Manifest domain.Manifest
}

// Result 下载结果。
type Result struct {
	Title         string
	Page          domain.Page
	ChosenQuality domain.Quality
	Codec         domain.Codec
	OutputPath    string
}

// Downloader 应用层用例：编排 元数据 → 播放地址 → 选流 → 下载 → 合并。
type Downloader struct {
	meta    MetadataSource
	streams StreamSource
	fetcher Fetcher
	muxer   Muxer
	log     *slog.Logger
}

// NewDownloader 构造（log 可空）。
func NewDownloader(meta MetadataSource, streams StreamSource, fetcher Fetcher, muxer Muxer, log *slog.Logger) *Downloader {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Downloader{meta: meta, streams: streams, fetcher: fetcher, muxer: muxer, log: log}
}

// Resolve 解析视频、分 P 与该 P 的 DASH 清单。
func (d *Downloader) Resolve(ctx context.Context, bvid string, page, qn int) (Info, error) {
	video, err := d.meta.View(ctx, bvid)
	if err != nil {
		return Info{}, fmt.Errorf("view: %w", err)
	}
	p, ok := video.PageByIndex(page)
	if !ok {
		return Info{}, domain.ErrPageNotFound
	}
	m, err := d.streams.Manifest(ctx, video.AID, p.CID, qn)
	if err != nil {
		return Info{}, fmt.Errorf("playurl: %w", err)
	}
	return Info{Video: video, Page: p, Manifest: m}, nil
}

// Download 执行完整下载并合并到 dst。
func (d *Downloader) Download(ctx context.Context, req Request, dst string) (Result, error) {
	info, err := d.Resolve(ctx, req.BVID, req.Page, req.QN)
	if err != nil {
		return Result{}, err
	}
	video, audio, chosen, err := domain.SelectStreams(info.Manifest, req.QN, req.PreferCodec, req.FallbackCodec)
	if err != nil {
		return Result{}, err
	}
	d.log.Info("selected",
		"bvid", req.BVID, "cid", info.Page.CID,
		"requestQN", req.QN, "chosenQN", chosen.QN, "quality", chosen.Name, "codec", video.Codec.String())

	tmp, err := os.MkdirTemp("", "bili2go-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)
	vPath := filepath.Join(tmp, "video.m4s")
	aPath := filepath.Join(tmp, "audio.m4s")

	if err := d.fetchBoth(ctx, video, audio, vPath, aPath); err != nil {
		return Result{}, err
	}
	if err := d.muxer.Mux(ctx, vPath, aPath, dst); err != nil {
		return Result{}, fmt.Errorf("mux: %w", err)
	}
	return Result{Title: info.Video.Title, Page: info.Page, ChosenQuality: chosen, Codec: video.Codec, OutputPath: dst}, nil
}

// fetchBoth 并发下载 video 与 audio；任一失败即取消另一路并返回该错误。
func (d *Downloader) fetchBoth(ctx context.Context, video, audio domain.Stream, vPath, aPath string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	jobs := []struct {
		s   domain.Stream
		dst string
		i   int
	}{{video, vPath, 0}, {audio, aPath, 1}}

	wg.Add(len(jobs))
	for _, j := range jobs {
		go func(s domain.Stream, dst string, i int) {
			defer wg.Done()
			if err := d.fetcher.Fetch(ctx, s.URLs, dst); err != nil {
				errs[i] = err
				cancel()
			}
		}(j.s, j.dst, j.i)
	}
	wg.Wait()

	for _, e := range errs {
		if e != nil {
			return fmt.Errorf("fetch: %w", e)
		}
	}
	return nil
}
