package httpapi

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"bili2go/internal/jobstore"
)

// 知识库 Web UI：浏览/检索历次分析、阅读 summary。服务端渲染，html/template 自动转义（防 XSS）。
// 需 serve -jobs-dir 启用（jobstore 即知识库存储）。

// kbResult 是从 job.Result（analyzeResp JSON）里取出的展示字段。
type kbResult struct {
	Title     string  `json:"title"`
	Markdown  string  `json:"markdown"`
	Engine    string  `json:"engine"`
	Segments  int     `json:"segments"`
	DurationS float64 `json:"duration_s"`
	Cached    bool    `json:"cached"`
}

func parseKBResult(raw json.RawMessage) kbResult {
	var r kbResult
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r)
	}
	return r
}

type kbRow struct {
	ID, BVID, Status, Title, Created string
}

var kbIndexTmpl = template.Must(template.New("idx").Parse(`<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>bili2go 知识库</title><style>
body{font:15px/1.6 -apple-system,system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;color:#222}
h1{font-size:1.4rem} form{margin:1rem 0} input[type=search]{width:70%;padding:.5rem;font-size:1rem}
button{padding:.5rem 1rem} table{border-collapse:collapse;width:100%} td,th{border-bottom:1px solid #eee;padding:.5rem;text-align:left;vertical-align:top}
.s-done{color:#137333} .s-error{color:#c5221f} .s-running,.s-queued{color:#b06000} a{color:#1a73e8;text-decoration:none} a:hover{text-decoration:underline}
.muted{color:#888;font-size:.85rem}</style></head><body>
<h1>bili2go 知识库</h1>
<form method="get" action="/kb"><input type="search" name="q" placeholder="搜索 summary / bvid…" value="{{.Query}}"><button>搜索</button></form>
<p class="muted">{{len .Jobs}} 条{{if .Query}}（匹配 “{{.Query}}”）{{end}}</p>
<table><tr><th>视频</th><th>标题</th><th>状态</th><th>时间</th></tr>
{{range .Jobs}}<tr>
<td><a href="/kb/{{.ID}}">{{.BVID}}</a></td>
<td>{{if .Title}}{{.Title}}{{else}}<span class="muted">—</span>{{end}}</td>
<td class="s-{{.Status}}">{{.Status}}</td>
<td class="muted">{{.Created}}</td>
</tr>{{else}}<tr><td colspan="4" class="muted">暂无分析记录</td></tr>{{end}}
</table></body></html>`))

var kbDetailTmpl = template.Must(template.New("detail").Parse(`<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{if .Title}}{{.Title}}{{else}}{{.BVID}}{{end}} — bili2go 知识库</title><style>
body{font:15px/1.6 -apple-system,system-ui,sans-serif;max-width:860px;margin:2rem auto;padding:0 1rem;color:#222}
a{color:#1a73e8;text-decoration:none} .muted{color:#888;font-size:.9rem}
pre{white-space:pre-wrap;word-wrap:break-word;background:#f6f8fa;padding:1rem;border-radius:8px;font:13px/1.6 ui-monospace,Menlo,monospace}
.s-error{color:#c5221f}</style></head><body>
<p><a href="/kb">← 知识库</a></p>
<h1>{{if .Title}}{{.Title}}{{else}}{{.BVID}}{{end}}</h1>
<p class="muted">{{.BVID}} · 状态 <b class="s-{{.Status}}">{{.Status}}</b>{{if .Engine}} · 转写 {{.Engine}} · {{.Segments}} 段 · {{printf "%.0f" .DurationS}}s{{end}}</p>
{{if .Error}}<pre class="s-error">{{.Error}}</pre>{{end}}
{{if .Markdown}}<pre>{{.Markdown}}</pre>{{else if not .Error}}<p class="muted">分析进行中或无结果，稍后刷新。</p>{{end}}
</body></html>`))

func (s *Server) handleKBIndex(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		http.Error(w, "knowledge base not enabled (start serve with -jobs-dir)", http.StatusNotImplemented)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var (
		list []jobstore.Job
		err  error
	)
	if q != "" {
		list, err = s.jobs.Search(q, 200)
	} else {
		list, err = s.jobs.List(200)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]kbRow, 0, len(list))
	for _, j := range list {
		rows = append(rows, kbRow{
			ID: j.ID, BVID: j.BVID, Status: string(j.Status),
			Title: parseKBResult(j.Result).Title, Created: j.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kbIndexTmpl.Execute(w, struct {
		Query string
		Jobs  []kbRow
	}{q, rows})
}

func (s *Server) handleKBDetail(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		http.Error(w, "knowledge base not enabled (start serve with -jobs-dir)", http.StatusNotImplemented)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/kb/")
	if id == "" {
		http.Redirect(w, r, "/kb", http.StatusSeeOther)
		return
	}
	j, ok, err := s.jobs.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	res := parseKBResult(j.Result)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = kbDetailTmpl.Execute(w, struct {
		ID, BVID, Status, Title, Markdown, Engine, Error string
		Segments                                         int
		DurationS                                        float64
	}{
		ID: j.ID, BVID: j.BVID, Status: string(j.Status), Title: res.Title,
		Markdown: res.Markdown, Engine: res.Engine, Error: j.Error,
		Segments: res.Segments, DurationS: res.DurationS,
	})
}
