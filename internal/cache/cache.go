// Package cache 提供磁盘内容缓存：LRU 淘汰 + single-flight 去重 + sidecar 元数据。
// 属基础设施层，领域/应用层不感知（tech-design §7.9）。
package cache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"bili2go/internal/domain"
)

// Meta 缓存条目元数据（命中即可设响应头，无需重解析）。
type Meta struct {
	Title   string
	Quality domain.Quality
	Codec   domain.Codec
}

// Entry 命中或产出的缓存条目。
type Entry struct {
	Path string
	Meta Meta
}

// ProduceFunc 在 miss 时把内容写入 dst 并返回元数据。
type ProduceFunc func(ctx context.Context, dst string) (Meta, error)

type node struct {
	key  string
	path string
	meta Meta
	size int64
	el   *list.Element
}

type sidecar struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	QN    int    `json:"qn"`
	Name  string `json:"name"`
	Codec string `json:"codec"`
}

type call struct {
	done  chan struct{}
	entry Entry
	err   error
}

// Cache 磁盘内容缓存。
type Cache struct {
	dir      string
	maxBytes int64

	mu     sync.Mutex
	items  map[string]*node
	lru    *list.List // front = 最近使用
	total  int64
	flight map[string]*call
}

// New 构造缓存并扫描 dir 采纳已有条目（跨重启存活）。
func New(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{
		dir:      dir,
		maxBytes: maxBytes,
		items:    map[string]*node{},
		lru:      list.New(),
		flight:   map[string]*call{},
	}
	c.scan()
	return c, nil
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) paths(key string) (mp4, side string) {
	h := hashKey(key)
	return filepath.Join(c.dir, h+".mp4"), filepath.Join(c.dir, h+".json")
}

// scan 采纳目录中已有 (sidecar + mp4)，按 mtime 恢复 LRU（旧的在后）。
func (c *Cache) scan() {
	des, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type found struct {
		key   string
		path  string
		meta  Meta
		size  int64
		mtime int64
	}
	var fs []found
	for _, de := range des {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		side := filepath.Join(c.dir, de.Name())
		b, err := os.ReadFile(side)
		if err != nil {
			continue
		}
		var sc sidecar
		if json.Unmarshal(b, &sc) != nil {
			continue
		}
		mp4 := side[:len(side)-len(".json")] + ".mp4"
		st, err := os.Stat(mp4)
		if err != nil {
			continue
		}
		codec, _ := domain.ParseCodec(sc.Codec)
		fs = append(fs, found{
			key:   sc.Key,
			path:  mp4,
			meta:  Meta{Title: sc.Title, Quality: domain.Quality{QN: sc.QN, Name: sc.Name}, Codec: codec},
			size:  st.Size(),
			mtime: st.ModTime().UnixNano(),
		})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mtime < fs[j].mtime }) // 旧的先入 → 处于队尾
	for _, f := range fs {
		n := &node{key: f.key, path: f.path, meta: f.meta, size: f.size}
		n.el = c.lru.PushFront(n)
		c.items[f.key] = n
		c.total += f.size
	}
	c.evictLocked()
}

// Get 命中直接返回；否则 single-flight 产出、落盘、入索引。
func (c *Cache) Get(ctx context.Context, key string, produce ProduceFunc) (Entry, error) {
	c.mu.Lock()
	if n, ok := c.items[key]; ok {
		c.lru.MoveToFront(n.el)
		e := Entry{Path: n.path, Meta: n.meta}
		c.mu.Unlock()
		return e, nil
	}
	if cl, ok := c.flight[key]; ok {
		c.mu.Unlock()
		<-cl.done
		return cl.entry, cl.err
	}
	cl := &call{done: make(chan struct{})}
	c.flight[key] = cl
	c.mu.Unlock()

	entry, size, err := c.produceStore(ctx, key, produce)

	c.mu.Lock()
	delete(c.flight, key)
	if err == nil {
		c.addLocked(key, entry, size)
	}
	c.mu.Unlock()

	cl.entry, cl.err = entry, err
	close(cl.done)
	return entry, err
}

// produceStore 在临时文件产出，成功后原子改名为缓存文件并写 sidecar。
func (c *Cache) produceStore(ctx context.Context, key string, produce ProduceFunc) (Entry, int64, error) {
	mp4, side := c.paths(key)
	tmp, err := os.CreateTemp(c.dir, "produce-*.tmp")
	if err != nil {
		return Entry{}, 0, err
	}
	tmpName := tmp.Name()
	tmp.Close()

	meta, err := produce(ctx, tmpName)
	if err != nil {
		os.Remove(tmpName)
		return Entry{}, 0, err
	}
	st, err := os.Stat(tmpName)
	if err != nil {
		os.Remove(tmpName)
		return Entry{}, 0, err
	}
	if err := os.Rename(tmpName, mp4); err != nil {
		os.Remove(tmpName)
		return Entry{}, 0, err
	}
	sc := sidecar{Key: key, Title: meta.Title, QN: meta.Quality.QN, Name: meta.Quality.Name, Codec: meta.Codec.String()}
	if b, e := json.Marshal(sc); e == nil {
		_ = os.WriteFile(side, b, 0o644)
	}
	return Entry{Path: mp4, Meta: meta}, st.Size(), nil
}

func (c *Cache) addLocked(key string, e Entry, size int64) {
	n := &node{key: key, path: e.Path, meta: e.Meta, size: size}
	n.el = c.lru.PushFront(n)
	c.items[key] = n
	c.total += size
	c.evictLocked()
}

// evictLocked 逐出最久未用条目直至不超预算（调用方持有 mu，或 New 单线程期）。
func (c *Cache) evictLocked() {
	for c.total > c.maxBytes && c.lru.Len() > 0 {
		back := c.lru.Back()
		n := back.Value.(*node)
		c.lru.Remove(back)
		delete(c.items, n.key)
		c.total -= n.size
		os.Remove(n.path)
		_, side := c.paths(n.key)
		os.Remove(side)
	}
}
