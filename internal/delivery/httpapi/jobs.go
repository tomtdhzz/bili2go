package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"bili2go/internal/domain"
	"bili2go/internal/jobstore"
)

// SetJobStore 注入任务存储并设 worker 数（<1 取 2）。需先 SetAnalyzer。
func (s *Server) SetJobStore(store jobstore.Store, workers int) {
	if workers < 1 {
		workers = 2
	}
	s.jobs = store
	s.jobWorkers = workers
	s.jobCh = make(chan string, 256)
}

// StartJobWorkers 启动 worker 直到 ctx 取消，并把残留 queued/running 任务重新入队（跨重启恢复）。
func (s *Server) StartJobWorkers(ctx context.Context) {
	if s.jobs == nil {
		return
	}
	for range s.jobWorkers {
		go s.jobWorker(ctx)
	}
	if list, err := s.jobs.List(0); err == nil {
		for _, j := range list {
			if j.Status == jobstore.StatusQueued || j.Status == jobstore.StatusRunning {
				s.enqueue(j.ID)
			}
		}
	}
}

func (s *Server) enqueue(id string) {
	select {
	case s.jobCh <- id:
	default: // 队列满：已持久化，下次恢复会补跑
	}
}

func (s *Server) jobWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.jobCh:
			s.runJob(ctx, id)
		}
	}
}

// runJob 跑一个任务：置 running → produceAnalyze → 存结果/错误。
func (s *Server) runJob(ctx context.Context, id string) {
	j, ok, err := s.jobs.Get(id)
	if err != nil || !ok {
		return
	}
	j.Status = jobstore.StatusRunning
	j.UpdatedAt = time.Now()
	_ = s.jobs.Save(j)

	jctx := ctx
	if s.cfg.DownloadTimeout > 0 {
		var cancel context.CancelFunc
		jctx, cancel = context.WithTimeout(ctx, s.cfg.DownloadTimeout)
		defer cancel()
	}
	prefer, _ := domain.ParseCodec(j.Codec)
	resp, perr := s.produceAnalyze(jctx, analyzeParamsT{bvid: j.BVID, page: j.Page, qn: j.QN, prefer: prefer})
	j.UpdatedAt = time.Now()
	if perr != nil {
		j.Status = jobstore.StatusError
		j.Error = perr.Error()
	} else {
		j.Status = jobstore.StatusDone
		if data, e := json.Marshal(resp); e == nil {
			j.Result = data
		}
	}
	_ = s.jobs.Save(j)
}

// handleJobs POST 创建异步分析任务；GET 列出最近任务（知识库浏览入口）。
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.analyzer == nil || s.jobs == nil {
		writeErr(w, http.StatusNotImplemented, -1, "jobs not configured")
		return
	}
	switch r.Method {
	case http.MethodPost:
		p, emsg := parseAnalyzeParams(r)
		if emsg != "" {
			writeErr(w, http.StatusBadRequest, -1, emsg)
			return
		}
		now := time.Now()
		j := jobstore.Job{
			ID: jobstore.NewID(), BVID: p.bvid, Page: p.page, QN: p.qn, Codec: p.prefer.String(),
			Status: jobstore.StatusQueued, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.jobs.Create(j); err != nil {
			writeErr(w, http.StatusInternalServerError, -1, err.Error())
			return
		}
		s.enqueue(j.ID)
		w.Header().Set("Location", "/api/jobs/"+j.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{"id": j.ID, "status": j.Status})
	case http.MethodGet:
		limit := atoiDefault(r.URL.Query().Get("limit"), 50)
		var (
			list []jobstore.Job
			err  error
		)
		if q := r.URL.Query().Get("q"); q != "" {
			list, err = s.jobs.Search(q, limit) // 知识库检索
		} else {
			list, err = s.jobs.List(limit)
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, -1, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	default:
		writeErr(w, http.StatusMethodNotAllowed, -1, "method not allowed")
	}
}

// handleJobByID GET /api/jobs/{id} 查任务状态与结果。
func (s *Server) handleJobByID(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeErr(w, http.StatusNotImplemented, -1, "jobs not configured")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	if id == "" {
		writeErr(w, http.StatusBadRequest, -1, "missing job id")
		return
	}
	j, ok, err := s.jobs.Get(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, -1, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, -1, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}
