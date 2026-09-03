package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

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
  bili2go serve [-addr :8080]

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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	srv := httpapi.New(buildDownloader(resolveSessdata(*sd)))
	fmt.Fprintf(os.Stderr, "bili2go serving on %s\n", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
