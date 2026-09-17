package jobstore

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDiskStoreLifecycle(t *testing.T) {
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j := Job{ID: NewID(), BVID: "BV1", Status: StatusQueued, CreatedAt: time.Now()}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(j); err == nil {
		t.Errorf("duplicate Create should fail")
	}

	got, ok, err := s.Get(j.ID)
	if err != nil || !ok || got.BVID != "BV1" || got.Status != StatusQueued {
		t.Fatalf("Get = %+v ok=%v err=%v", got, ok, err)
	}

	j.Status = StatusDone
	j.Result = json.RawMessage(`{"markdown":"x"}`)
	if err := s.Save(j); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Get(j.ID)
	var resm map[string]string
	if err := json.Unmarshal(got.Result, &resm); err != nil || got.Status != StatusDone || resm["markdown"] != "x" {
		t.Errorf("after Save = %+v (result parse err=%v)", got, err)
	}

	if _, ok, _ := s.Get("missing"); ok {
		t.Errorf("missing id should not be found")
	}

	list, _ := s.List(10)
	if len(list) != 1 || list[0].ID != j.ID {
		t.Errorf("List = %+v", list)
	}
}

func TestDiskStoreListOrder(t *testing.T) {
	s, _ := NewDiskStore(t.TempDir())
	now := time.Now()
	old := Job{ID: "a", BVID: "old", Status: StatusDone, CreatedAt: now.Add(-time.Hour)}
	recent := Job{ID: "b", BVID: "new", Status: StatusDone, CreatedAt: now}
	_ = s.Create(old)
	_ = s.Create(recent)
	list, _ := s.List(0)
	if len(list) != 2 || list[0].ID != "b" {
		t.Errorf("List newest-first failed: %+v", list)
	}
}

func TestDiskStoreSearch(t *testing.T) {
	s, _ := NewDiskStore(t.TempDir())
	_ = s.Create(Job{ID: "1", BVID: "BVaaa", Result: json.RawMessage(`{"markdown":"增长复盘 GMV 同比 38%"}`), CreatedAt: time.Now()})
	_ = s.Create(Job{ID: "2", BVID: "BVbbb", Result: json.RawMessage(`{"markdown":"美食 vlog"}`), CreatedAt: time.Now()})

	if hits, _ := s.Search("GMV", 10); len(hits) != 1 || hits[0].ID != "1" {
		t.Errorf("search 'GMV' = %+v", hits)
	}
	if hits, _ := s.Search("bvbbb", 10); len(hits) != 1 || hits[0].ID != "2" { // 大小写不敏感
		t.Errorf("search bvid = %+v", hits)
	}
	if hits, _ := s.Search("不存在xyz", 10); len(hits) != 0 {
		t.Errorf("no-match should be empty: %+v", hits)
	}
	if all, _ := s.Search("", 10); len(all) != 2 {
		t.Errorf("empty query returns all: %+v", all)
	}
}
