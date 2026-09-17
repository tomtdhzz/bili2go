// Package jobstore 持久化「分析任务」（异步 job）：状态 + 结果。
//
// 默认磁盘实现（每个 job 一个 JSON 文件，原子写），零依赖、跨重启存活、可列举。
// Store 是接口——将来要换 SQLite/Postgres 只需实现它，调用方不变。
package jobstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status 是任务生命周期状态。
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusError   Status = "error"
)

// Job 是一次分析任务的完整记录。Result 为完成时的分析响应 JSON（不耦合交付层类型）。
type Job struct {
	ID        string          `json:"id"`
	BVID      string          `json:"bvid"`
	Page      int             `json:"page"`
	QN        int             `json:"qn"`
	Codec     string          `json:"codec"`
	Status    Status          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Store 是任务存储抽象。
type Store interface {
	Create(j Job) error
	Get(id string) (Job, bool, error)
	Save(j Job) error
	List(limit int) ([]Job, error)
	// Search 在已存任务的 bvid + 结果(含 summary/title)里子串检索（大小写不敏感）。
	Search(query string, limit int) ([]Job, error)
}

// NewID 生成随机 16 hex 任务 id。
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// DiskStore 把每个 job 存成 <dir>/<id>.json。
type DiskStore struct {
	dir string
	mu  sync.RWMutex
}

// NewDiskStore 构造并确保目录存在。
func NewDiskStore(dir string) (*DiskStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DiskStore{dir: dir}, nil
}

func (s *DiskStore) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *DiskStore) write(j Job) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(j.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(j.ID)) // 原子替换
}

// Create 落一条新任务（id 已存在则报错）。
func (s *DiskStore) Create(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(j.ID)); err == nil {
		return errors.New("job already exists: " + j.ID)
	}
	return s.write(j)
}

// Save 覆盖写（更新状态/结果）。
func (s *DiskStore) Save(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(j)
}

// Get 按 id 读；不存在返回 ok=false。
func (s *DiskStore) Get(id string) (Job, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

// List 返回最近 limit 条（按创建时间倒序）；limit<=0 返回全部。
func (s *DiskStore) List(limit int) ([]Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	files, _ := filepath.Glob(filepath.Join(s.dir, "*.json"))
	jobs := make([]Job, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(data, &j) == nil && j.ID != "" {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, k int) bool { return jobs[i].CreatedAt.After(jobs[k].CreatedAt) })
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

// Search 在 bvid + 结果 JSON（含 summary markdown/title）里做大小写不敏感子串检索，倒序。
func (s *DiskStore) Search(query string, limit int) ([]Job, error) {
	all, err := s.List(0)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if limit > 0 && len(all) > limit {
			return all[:limit], nil
		}
		return all, nil
	}
	out := []Job{}
	for _, j := range all {
		hay := strings.ToLower(j.BVID + " " + string(j.Result))
		if strings.Contains(hay, q) {
			out = append(out, j)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
