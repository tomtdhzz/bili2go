package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bili2go/internal/analyze"
	"bili2go/internal/app"
	"bili2go/internal/bilibili"
	"bili2go/internal/delivery/httpapi"
	"bili2go/internal/domain"
	"bili2go/internal/media"
)

// Run 分发子命令，返回进程退出码。
func Run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "info":
		return cmdInfo(args[1:])
	case "dl":
		return cmdDownload(args[1:])
	case "serve":
		return cmdServe(args[1:])
	case "analyze":
		return cmdAnalyze(args[1:])
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `bili2go — Bilibili DASH 视频下载器

用法:
  bili2go info  -bvid <BV|url> [-page N]
  bili2go dl    -bvid <BV|url> [-page N] [-qn 80] [-codec avc] -o out.mp4
  bili2go serve    [-addr :8080]
  bili2go analyze  -bvid <BV|url> [-o out.mp4] | -video <path> [-summarizer URL]

SESSDATA: 环境变量 BILI_SESSDATA 或各子命令的 -sessdata
`)
}

func buildDownloader(sessdata string) *app.Downloader {
	c := bilibili.NewClient(sessdata)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return app.NewDownloader(
		bilibili.NewMetadata(c),
		bilibili.NewPlayURL(c),
		media.NewHTTPFetcher(c),
		media.NewFFmpegMuxer(),
		log,
	)
}

func resolveSessdata(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv("BILI_SESSDATA")
}

func cmdInfo(args []string) int {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	bvid := fs.String("bvid", "", "BV 号或视频 URL")
	page := fs.Int("page", 0, "分 P（默认 1）")
	qn := fs.Int("qn", 0, "目标清晰度码（默认取可用最高）")
	codec := fs.String("codec", "avc", "期望编码 avc/hevc/av1")
	sd := fs.String("sessdata", "", "SESSDATA（默认取环境变量 BILI_SESSDATA）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bvid == "" {
		fmt.Fprintln(os.Stderr, "missing -bvid")
		return 2
	}
	prefer, ok := domain.ParseCodec(*codec)
	if !ok {
		fmt.Fprintln(os.Stderr, "invalid -codec (avc/hevc/av1)")
		return 2
	}
	dl := buildDownloader(resolveSessdata(*sd))
	info, err := dl.Resolve(context.Background(), *bvid, *page, *qn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	video, _, cur, err := domain.SelectStreams(info.Manifest, *qn, prefer, domain.CodecAVC)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out := map[string]any{
		"bvid":      info.Video.BVID,
		"aid":       info.Video.AID,
		"cid":       info.Page.CID,
		"page":      info.Page.Index,
		"title":     info.Video.Title,
		"duration":  int(info.Page.Duration.Seconds()),
		"current":   map[string]any{"qn": cur.QN, "name": cur.Name, "codec": video.Codec.String()},
		"qualities": info.Manifest.Available,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return 0
}

func cmdDownload(args []string) int {
	fs := flag.NewFlagSet("dl", flag.ContinueOnError)
	bvid := fs.String("bvid", "", "BV 号或视频 URL")
	page := fs.Int("page", 0, "分 P（默认 1）")
	qn := fs.Int("qn", 0, "清晰度码（默认取可用最高）")
	codec := fs.String("codec", "avc", "编码 avc/hevc/av1")
	out := fs.String("o", "", "输出 mp4 路径")
	sd := fs.String("sessdata", "", "SESSDATA（默认取环境变量 BILI_SESSDATA）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bvid == "" {
		fmt.Fprintln(os.Stderr, "missing -bvid")
		return 2
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "missing -o")
		return 2
	}
	prefer, ok := domain.ParseCodec(*codec)
	if !ok {
		fmt.Fprintln(os.Stderr, "invalid -codec (avc/hevc/av1)")
		return 2
	}
	dl := buildDownloader(resolveSessdata(*sd))
	res, err := dl.Download(context.Background(), app.Request{
		BVID: *bvid, Page: *page, QN: *qn,
		PreferCodec: prefer, FallbackCodec: domain.CodecAVC,
	}, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("resolved cid=%d quality=%s codec=%s -> %s\n",
		res.Page.CID, res.ChosenQuality.Name, res.Codec.String(), res.OutputPath)
	return 0
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "监听地址")
	sd := fs.String("sessdata", "", "SESSDATA（默认取环境变量 BILI_SESSDATA）")
	cfg := httpapi.DefaultConfig()
	fs.IntVar(&cfg.MaxConcurrent, "max-concurrent", cfg.MaxConcurrent, "同时进行的下载数上限")
	fs.DurationVar(&cfg.MaxWait, "max-wait", cfg.MaxWait, "排队等待空位上限（0=不排队）")
	fs.DurationVar(&cfg.DownloadTimeout, "download-timeout", cfg.DownloadTimeout, "单次下载超时（0=不限）")
	fs.DurationVar(&cfg.ShutdownGrace, "shutdown-grace", cfg.ShutdownGrace, "优雅关闭排空上限")
	fs.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "缓存目录")
	fs.Int64Var(&cfg.CacheMaxBytes, "cache-size", cfg.CacheMaxBytes, "缓存总字节上限（0=禁用缓存与去重）")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	srv := httpapi.New(buildDownloader(resolveSessdata(*sd)), cfg)
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 不设 WriteTimeout：长下载由 per-request ctx 超时控制（tech-design §7.8）
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "bili2go serving on %s\n", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		fmt.Fprintln(os.Stderr, err)
		return 1
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "shutting down…")
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
		defer cancel()
		if err := httpSrv.Shutdown(shutCtx); err != nil {
			fmt.Fprintln(os.Stderr, "graceful shutdown timed out:", err)
			return 1
		}
		return 0
	}
}

func cmdAnalyze(args []string) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	bvid := fs.String("bvid", "", "BV 号或视频 URL（下载后分析；与 -video 二选一）")
	video := fs.String("video", "", "分析已存在的本地视频文件（跳过下载）")
	out := fs.String("o", "", "下载输出 mp4 路径（-bvid 模式；留空则用临时文件，分析后删除，除非 -keep-video）")
	page := fs.Int("page", 0, "分 P（默认 1）")
	qn := fs.Int("qn", 0, "清晰度码（默认取可用最高）")
	codec := fs.String("codec", "avc", "编码 avc/hevc/av1")
	sd := fs.String("sessdata", "", "SESSDATA（默认取环境变量 BILI_SESSDATA）")
	digestBin := fs.String("digest-bin", "", "video-digest 二进制路径（默认取环境变量 VIDEO_DIGEST_BIN，否则 PATH 中的 video-digest）")
	summarizer := fs.String("summarizer", "", "摘要服务地址（默认取环境变量 BILI_SUMMARIZER_URL，否则 http://127.0.0.1:8091）")
	lang := fs.String("lang", "zh-CN", "转写 locale")
	keywords := fs.String("keywords", "unspoken", "画面关键字模式 unspoken/all")
	digestOut := fs.String("digest-out", "", "分析产物目录（默认 <video 同目录>/<stem>.digest/）")
	top := fs.Int("top", 25, "摘要中画面关键字取前 N 条")
	keepVideo := fs.Bool("keep-video", false, "分析后保留下载的视频（默认下载到临时文件并删除）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *video == "" && *bvid == "" {
		fmt.Fprintln(os.Stderr, "need -video <path> or -bvid <BV|url>")
		return 2
	}
	prefer, ok := domain.ParseCodec(*codec)
	if !ok {
		fmt.Fprintln(os.Stderr, "invalid -codec (avc/hevc/av1)")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	summarizerURL := *summarizer
	if summarizerURL == "" {
		summarizerURL = os.Getenv("BILI_SUMMARIZER_URL")
	}
	sum := analyze.NewHTTPSummarizer(summarizerURL, *top)
	// 前置探活：在昂贵的下载 + digest 之前确认摘要服务可用，避免白跑（requirement-orchestrator target-system preflight）。
	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	healthErr := sum.Health(hctx)
	hcancel()
	if healthErr != nil {
		fmt.Fprintln(os.Stderr, healthErr)
		fmt.Fprintln(os.Stderr, "hint: 先启动摘要服务：cd deploy && docker compose up -d --build")
		return 1
	}

	videoPath := *video
	var cleanup func()
	if videoPath == "" {
		dst := *out
		removeAfter := false
		if dst == "" {
			f, err := os.CreateTemp("", "bili2go-analyze-*.mp4")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			dst = f.Name()
			f.Close()
			removeAfter = !*keepVideo
		}
		dl := buildDownloader(resolveSessdata(*sd))
		res, err := dl.Download(ctx, app.Request{
			BVID: *bvid, Page: *page, QN: *qn,
			PreferCodec: prefer, FallbackCodec: domain.CodecAVC,
		}, dst)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "downloaded %s (%s/%s) -> %s\n",
			res.Title, res.ChosenQuality.Name, res.Codec.String(), dst)
		videoPath = dst
		if removeAfter {
			cleanup = func() { os.Remove(dst) }
		}
	}
	if cleanup != nil {
		defer cleanup()
	}

	digester := analyze.NewExecDigester(envOr(*digestBin, "VIDEO_DIGEST_BIN"))
	digester.Lang = *lang
	digester.Keywords = *keywords
	az := analyze.NewAnalyzer(digester, sum)

	rep, err := az.Analyze(ctx, videoPath, *digestOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	m := rep.Digest.Meta
	fmt.Fprintf(os.Stderr, "analyzed: 时长%.0fs 音轨%v 转写%s/%d段 关键字%d gaps%d model=%s fallback=%v\n",
		m.DurationS, m.HasAudio, m.Engine, m.Segments, m.Keywords, m.Gaps, rep.Summary.Model, rep.Summary.Fallback)
	if rep.Summary.Reason != "" {
		fmt.Fprintln(os.Stderr, "note:", rep.Summary.Reason)
	}
	fmt.Println(rep.SummaryPath)
	return 0
}

func envOr(flagVal, key string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(key)
}
