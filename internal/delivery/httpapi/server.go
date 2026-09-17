package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bili2go/internal/analyze"
	"bili2go/internal/app"
	"bili2go/internal/bilibili"
	"bili2go/internal/cache"
	"bili2go/internal/domain"
)

// Server 是 HTTP 交付层，依赖应用层 Downloader 用例。
type Server struct {
	dl         *app.Downloader
	analyzer   *analyze.Analyzer
	limiter    *Limiter
	cache      *cache.Cache
	cfg        Config
	retryAfter int
}

// Config 交付层过载保护与超时配置（tech-design §7.8）。
type Config struct {
	MaxConcurrent   int
	MaxWait         time.Duration
	DownloadTimeout time.Duration
	ShutdownGrace   time.Duration
	CacheDir        string
	CacheMaxBytes   int64
	ArtifactDir     string // 非空时 /api/analyze 把 mp4+产物按 bvid 存此目录（否则临时+删）
}

// DefaultConfig 单实例默认：4 并发、5s 排队、30m 单次超时、30s 关闭排空。
func DefaultConfig() Config {
	return Config{
		MaxConcurrent:   4,
		MaxWait:         5 * time.Second,
		DownloadTimeout: 30 * time.Minute,
		ShutdownGrace:   30 * time.Second,
		CacheDir:        filepath.Join(os.TempDir(), "bili2go-cache"),
		CacheMaxBytes:   2 << 30,
	}
}

// New 构造服务器。
func New(dl *app.Downloader, cfg Config) *Server {
	ra := int(cfg.MaxWait / time.Second)
	if ra < 1 {
		ra = 1
	}
	s := &Server{
		dl:         dl,
		limiter:    NewLimiter(cfg.MaxConcurrent, cfg.MaxWait),
		cfg:        cfg,
		retryAfter: ra,
	}
	if cfg.CacheMaxBytes > 0 {
		if c, err := cache.New(cfg.CacheDir, cfg.CacheMaxBytes); err != nil {
			fmt.Fprintf(os.Stderr, "cache disabled: %v\n", err)
		} else {
			s.cache = c
		}
	}
	return s
}

// Handler 返回路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/download", s.handleDownload)
	mux.HandleFunc("/api/analyze", s.handleAnalyze)
	return mux
}

type qualityJSON struct {
	QN   int    `json:"qn"`
	Name string `json:"name"`
}

type currentJSON struct {
	QN    int    `json:"qn"`
	Name  string `json:"name"`
	Codec string `json:"codec"`
}

type infoJSON struct {
	BVID      string        `json:"bvid"`
	AID       int64         `json:"aid"`
	CID       int64         `json:"cid"`
	Page      int           `json:"page"`
	Title     string        `json:"title"`
	Duration  int           `json:"duration"`
	Current   currentJSON   `json:"current"`
	Qualities []qualityJSON `json:"qualities"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bvid := q.Get("bvid")
	if bvid == "" {
		writeErr(w, http.StatusBadRequest, -1, "missing bvid")
		return
	}
	page := atoiDefault(q.Get("page"), 0)
	qn := atoiDefault(q.Get("qn"), 0)

	info, err := s.dl.Resolve(r.Context(), bvid, page, qn)
	if err != nil {
		mapErr(w, err)
		return
	}
	video, _, chosen, err := domain.SelectStreams(info.Manifest, qn, domain.CodecAVC, domain.CodecHEVC)
	if err != nil {
		mapErr(w, err)
		return
	}
	resp := infoJSON{
		BVID:     info.Video.BVID,
		AID:      info.Video.AID,
		CID:      info.Page.CID,
		Page:     info.Page.Index,
		Title:    info.Video.Title,
		Duration: int(info.Page.Duration.Seconds()),
		Current:  currentJSON{QN: chosen.QN, Name: chosen.Name, Codec: video.Codec.String()},
	}
	for _, a := range info.Manifest.Available {
		resp.Qualities = append(resp.Qualities, qualityJSON{QN: a.QN, Name: a.Name})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bvid := q.Get("bvid")
	if bvid == "" {
		writeErr(w, http.StatusBadRequest, -1, "missing bvid")
		return
	}
	page := atoiDefault(q.Get("page"), 0)
	qn := atoiDefault(q.Get("qn"), 0)
	prefer, ok := domain.ParseCodec(q.Get("codec"))
	if !ok {
		writeErr(w, http.StatusBadRequest, -1, "invalid codec")
		return
	}

	// produce（仅 miss 路径执行）：限流 + 单次超时 + 下载合并到 dst。
	produce := func(ctx context.Context, dst string) (cache.Meta, error) {
		release, err := s.limiter.Acquire(ctx)
		if err != nil {
			return cache.Meta{}, err
		}
		defer release()
		if s.cfg.DownloadTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.cfg.DownloadTimeout)
			defer cancel()
		}
		res, err := s.dl.Download(ctx, app.Request{
			BVID: bvid, Page: page, QN: qn,
			PreferCodec: prefer, FallbackCodec: domain.CodecAVC,
		}, dst)
		if err != nil {
			return cache.Meta{}, err
		}
		return cache.Meta{Title: res.Title, Quality: res.ChosenQuality, Codec: res.Codec}, nil
	}

	var entry cache.Entry
	var err error
	if s.cache != nil {
		entry, err = s.cache.Get(r.Context(), cacheKey(bvid, page, qn, prefer), produce)
	} else {
		var tmp *os.File
		if tmp, err = os.CreateTemp("", "bili2go-dl-*.mp4"); err == nil {
			path := tmp.Name()
			tmp.Close()
			defer os.Remove(path)
			var meta cache.Meta
			meta, err = produce(r.Context(), path)
			entry = cache.Entry{Path: path, Meta: meta}
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrBusy):
			w.Header().Set("Retry-After", strconv.Itoa(s.retryAfter))
			writeErr(w, http.StatusTooManyRequests, -429, "server busy")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// 客户端断开或超时：无需响应
		default:
			mapErr(w, err)
		}
		return
	}
	s.serveFile(w, entry, bvid)
}

// SetAnalyzer 注入分析用例（可空；未注入时 /api/analyze 返回 501）。
func (s *Server) SetAnalyzer(a *analyze.Analyzer) { s.analyzer = a }

type analyzeResp struct {
	Markdown    string  `json:"markdown"`
	Model       string  `json:"model"`
	Fallback    bool    `json:"fallback"`
	Reason      string  `json:"reason,omitempty"`
	Title       string  `json:"title"`
	Page        int     `json:"page"`
	Quality     int     `json:"quality"`
	Codec       string  `json:"codec"`
	Engine      string  `json:"engine"`
	Segments    int     `json:"segments"`
	Duration    float64 `json:"duration_s"`
	VideoPath   string  `json:"video_path,omitempty"`
	SummaryPath string  `json:"summary_path,omitempty"`
	DigestPath  string  `json:"digest_path,omitempty"`
	Cached      bool    `json:"cached,omitempty"`
}

// handleAnalyze 下载 → digest → 摘要，返回五节 summary 的 JSON。重操作，走限流。
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.analyzer == nil {
		writeErr(w, http.StatusNotImplemented, -1, "analyze not configured")
		return
	}
	q := r.URL.Query()
	bvid := q.Get("bvid")
	if bvid == "" {
		writeErr(w, http.StatusBadRequest, -1, "missing bvid")
		return
	}
	page := atoiDefault(q.Get("page"), 0)
	qn := atoiDefault(q.Get("qn"), 0)
	prefer, ok := domain.ParseCodec(q.Get("codec"))
	if !ok {
		writeErr(w, http.StatusBadRequest, -1, "invalid codec")
		return
	}

	// 服务端留存目录（若配置）：命中已存 result.json 直接复用，不下载/转写/占限流槽。
	var artifactDir string
	if s.cfg.ArtifactDir != "" {
		key := sanitizeFilename(bvid)
		if key == "" {
			key = "video"
		}
		if page > 0 {
			key = fmt.Sprintf("%s-p%d", key, page)
		}
		artifactDir = filepath.Join(s.cfg.ArtifactDir, key)
		if data, rerr := os.ReadFile(filepath.Join(artifactDir, "result.json")); rerr == nil {
			var cached analyzeResp
			if json.Unmarshal(data, &cached) == nil && cached.Markdown != "" {
				cached.Cached = true
				writeJSON(w, http.StatusOK, cached)
				return
			}
		}
	}

	release, err := s.limiter.Acquire(r.Context())
	if err != nil {
		if errors.Is(err, ErrBusy) {
			w.Header().Set("Retry-After", strconv.Itoa(s.retryAfter))
			writeErr(w, http.StatusTooManyRequests, -429, "server busy")
		}
		return // 客户端断开：无需响应
	}
	defer release()

	ctx := r.Context()
	if s.cfg.DownloadTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.DownloadTimeout)
		defer cancel()
	}

	var videoPath, outDir string
	if artifactDir != "" {
		outDir = artifactDir
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, -1, err.Error())
			return
		}
		videoPath = filepath.Join(outDir, "video.mp4")
	} else {
		tmp, terr := os.CreateTemp("", "bili2go-az-*.mp4")
		if terr != nil {
			writeErr(w, http.StatusInternalServerError, -1, terr.Error())
			return
		}
		videoPath = tmp.Name()
		tmp.Close()
		defer os.Remove(videoPath)
		if outDir, terr = os.MkdirTemp("", "bili2go-digest-*"); terr != nil {
			writeErr(w, http.StatusInternalServerError, -1, terr.Error())
			return
		}
		defer os.RemoveAll(outDir)
	}

	res, err := s.dl.Download(ctx, app.Request{
		BVID: bvid, Page: page, QN: qn,
		PreferCodec: prefer, FallbackCodec: domain.CodecAVC,
	}, videoPath)
	if err != nil {
		analyzeErr(w, err)
		return
	}

	report, err := s.analyzer.Analyze(ctx, videoPath, outDir)
	if err != nil {
		analyzeErr(w, err)
		return
	}
	resp := analyzeResp{
		Markdown: report.Summary.Markdown,
		Model:    report.Summary.Model,
		Fallback: report.Summary.Fallback,
		Reason:   report.Summary.Reason,
		Title:    res.Title,
		Page:     page,
		Quality:  res.ChosenQuality.QN,
		Codec:    res.Codec.String(),
		Engine:   report.Digest.Meta.Engine,
		Segments: report.Digest.Meta.Segments,
		Duration: report.Digest.Meta.DurationS,
	}
	if artifactDir != "" {
		resp.VideoPath = videoPath
		resp.SummaryPath = report.SummaryPath
		resp.DigestPath = report.Digest.DigestPath
		if data, merr := json.Marshal(resp); merr == nil {
			_ = os.WriteFile(filepath.Join(artifactDir, "result.json"), data, 0o644)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// analyzeErr 把下载/分析错误映射为 HTTP；客户端断开/超时不回响应。
func analyzeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return
	default:
		mapErr(w, err)
	}
}

// cacheKey 由请求参数构造缓存键。
func cacheKey(bvid string, page, qn int, codec domain.Codec) string {
	return fmt.Sprintf("%s|p%d|qn%d|%s", bvid, page, qn, codec.String())
}

// serveFile 回传文件并设响应头（命中/产出统一走此）。
func (s *Server) serveFile(w http.ResponseWriter, e cache.Entry, bvid string) {
	f, err := os.Open(e.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, -1, err.Error())
		return
	}
	defer f.Close()
	name := sanitizeFilename(e.Meta.Title)
	if name == "" {
		name = bvid
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+".mp4\"")
	w.Header().Set("X-Bili-Quality", strconv.Itoa(e.Meta.Quality.QN))
	w.Header().Set("X-Bili-Codec", e.Meta.Codec.String())
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// --- helpers ---

type errJSON struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, errJSON{Code: code, Message: msg})
}

// mapErr 把领域/上游错误映射为 HTTP 状态码 + 业务码。
func mapErr(w http.ResponseWriter, err error) {
	var apiErr *bilibili.APIError
	switch {
	case errors.Is(err, domain.ErrPageNotFound):
		writeErr(w, http.StatusNotFound, -404, "page not found")
	case errors.As(err, &apiErr):
		writeErr(w, http.StatusBadGateway, apiErr.Code, apiErr.Message)
	default:
		writeErr(w, http.StatusBadGateway, -502, err.Error())
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// sanitizeFilename 去除文件名中的路径分隔符与引号等不安全字符。
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", "\"", "", ":", "_",
		"*", "_", "?", "_", "<", "_", ">", "_", "|", "_", "\n", " ", "\r", " ",
	)
	return strings.TrimSpace(replacer.Replace(s))
}
