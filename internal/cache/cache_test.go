package cache

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bili2go/internal/domain"
)

func sampleMeta() Meta {
	return Meta{Title: "t", Quality: domain.NewQuality(80), Codec: domain.CodecAVC}
}

func writeProduce(content string, calls *int32) ProduceFunc {
	return func(ctx context.Context, dst string) (Meta, error) {
		atomic.AddInt32(calls, 1)
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return Meta{}, err
		}
		return sampleMeta(), nil
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGetProducesThenHits(t *testing.T) {
	c, err := New(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	var calls int32
	e1, err := c.Get(context.Background(), "k1", writeProduce("data", &calls))
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, e1.Path) != "data" {
		t.Errorf("content = %q", readFile(t, e1.Path))
	}
	if e1.Meta.Quality.QN != 80 || e1.Meta.Codec != domain.CodecAVC {
		t.Errorf("meta = %+v", e1.Meta)
	}
	e2, err := c.Get(context.Background(), "k1", writeProduce("SHOULD-NOT-RUN", &calls))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1 (2nd should hit)", calls)
	}
	if e2.Path != e1.Path {
		t.Errorf("hit path %q != %q", e2.Path, e1.Path)
	}
}

func TestGetDedupsConcurrent(t *testing.T) {
	c, _ := New(t.TempDir(), 1<<30)
	var calls int32
	rel := make(chan struct{})
	produce := func(ctx context.Context, dst string) (Meta, error) {
		atomic.AddInt32(&calls, 1)
		<-rel
		return sampleMeta(), os.WriteFile(dst, []byte("x"), 0o644)
	}
	const n = 6
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() { defer wg.Done(); _, _ = c.Get(context.Background(), "k", produce) }()
	}
	time.Sleep(50 * time.Millisecond) // let them coalesce on the single flight
	close(rel)
	wg.Wait()
	if calls != 1 {
		t.Errorf("produce calls = %d, want 1 (single-flight)", calls)
	}
}

func TestDistinctKeys(t *testing.T) {
	c, _ := New(t.TempDir(), 1<<30)
	var calls int32
	_, _ = c.Get(context.Background(), "a", writeProduce("A", &calls))
	_, _ = c.Get(context.Background(), "b", writeProduce("B", &calls))
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (distinct keys)", calls)
	}
}

func TestEvictionLRU(t *testing.T) {
	c, _ := New(t.TempDir(), 6) // holds ~1 entry of "data"(4B); 2nd add evicts 1st
	var calls int32
	e1, _ := c.Get(context.Background(), "k1", writeProduce("data", &calls))
	_, _ = c.Get(context.Background(), "k2", writeProduce("data", &calls)) // total 8 > 6 → evict k1
	if _, err := os.Stat(e1.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("k1 should be evicted, stat err = %v", err)
	}
	_, _ = c.Get(context.Background(), "k1", writeProduce("data", &calls)) // miss → re-produce
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (k1 re-produced after eviction)", calls)
	}
}

func TestRestartAdopts(t *testing.T) {
	dir := t.TempDir()
	c1, _ := New(dir, 1<<30)
	var calls int32
	if _, err := c1.Get(context.Background(), "k", writeProduce("data", &calls)); err != nil {
		t.Fatal(err)
	}
	c2, err := New(dir, 1<<30) // "restart"
	if err != nil {
		t.Fatal(err)
	}
	e, err := c2.Get(context.Background(), "k", func(ctx context.Context, dst string) (Meta, error) {
		t.Error("produce ran; cache should have adopted the existing file")
		return Meta{}, errors.New("should not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, e.Path) != "data" {
		t.Errorf("adopted content = %q", readFile(t, e.Path))
	}
	if e.Meta.Quality.QN != 80 || e.Meta.Codec != domain.CodecAVC {
		t.Errorf("adopted meta lost: %+v", e.Meta)
	}
}

func TestProduceErrorNotCached(t *testing.T) {
	c, _ := New(t.TempDir(), 1<<30)
	var calls int32
	boom := errors.New("boom")
	fail := func(ctx context.Context, dst string) (Meta, error) {
		atomic.AddInt32(&calls, 1)
		return Meta{}, boom
	}
	if _, err := c.Get(context.Background(), "k", fail); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, err := c.Get(context.Background(), "k", fail); err == nil {
		t.Fatal("want error again (not cached)")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (error not cached)", calls)
	}
}
