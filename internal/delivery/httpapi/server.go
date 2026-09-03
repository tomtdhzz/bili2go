package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"bili2go/internal/app"
	"bili2go/internal/bilibili"
	"bili2go/internal/domain"
)

// Server 是 HTTP 交付层，依赖应用层 Downloader 用例。
type Server struct {
	dl         *app.Downloader
	limiter    *Limiter
	cfg        Config
	retryAfter int
}

// Config 交付层过载保护与超时配置（tech-design §7.8）。
type Config struct {
	MaxConcurrent   int
	MaxWait         time.Duration
	DownloadTimeout time.Duration
	ShutdownGrace   time.Duration
}

// DefaultConfig 单实例默认：4 并发、5s 排队、30m 单次超时、30s 关闭排空。
func DefaultConfig() Config {
	return Config{
		MaxConcurrent:   4,
		MaxWait:         5 * time.Second,
		DownloadTimeout: 30 * time.Minute,
		ShutdownGrace:   30 * time.Second,
	}
}

// New 构造服务器。
func New(dl *app.Downloader, cfg Config) *Server {
	ra := int(cfg.MaxWait / time.Second)
	if ra < 1 {
		ra = 1
	}
	return &Server{
		dl:         dl,
		limiter:    NewLimiter(cfg.MaxConcurrent, cfg.MaxWait),
		cfg:        cfg,
		retryAfter: ra,
	}
}

// Handler 返回路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/download", s.handleDownload)
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

	release, err := s.limiter.Acquire(r.Context())
	if err != nil {
		if errors.Is(err, ErrBusy) {
			w.Header().Set("Retry-After", strconv.Itoa(s.retryAfter))
			writeErr(w, http.StatusTooManyRequests, -429, "server busy")
		}
		return // ctx 取消 → 客户端已断开，无需响应
	}
	defer release()

	ctx := r.Context()
	if s.cfg.DownloadTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.DownloadTimeout)
		defer cancel()
	}

	tmp, err := os.CreateTemp("", "bili2go-dl-*.mp4")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, -1, err.Error())
		return
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	res, err := s.dl.Download(ctx, app.Request{
		BVID: bvid, Page: page, QN: qn,
		PreferCodec: prefer, FallbackCodec: domain.CodecAVC,
	}, tmpName)
	if err != nil {
		mapErr(w, err)
		return
	}
	f, err := os.Open(tmpName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, -1, err.Error())
		return
	}
	defer f.Close()

	name := sanitizeFilename(res.Title)
	if name == "" {
		name = bvid
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+".mp4\"")
	w.Header().Set("X-Bili-Quality", strconv.Itoa(res.ChosenQuality.QN))
	w.Header().Set("X-Bili-Codec", res.Codec.String())
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
