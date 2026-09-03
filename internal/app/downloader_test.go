package app

import (
	"context"
	"os"
	"sync"
	"testing"

	"bili2go/internal/domain"
)

type fakeMeta struct {
	v   domain.Video
	err error
}

func (f fakeMeta) View(ctx context.Context, bvid string) (domain.Video, error) {
	return f.v, f.err
}

type fakeStreams struct {
	m      domain.Manifest
	gotAID int64
	gotCID int64
	gotQN  int
	err    error
}

func (f *fakeStreams) Manifest(ctx context.Context, aid, cid int64, qn int) (domain.Manifest, error) {
	f.gotAID, f.gotCID, f.gotQN = aid, cid, qn
	return f.m, f.err
}

type fakeFetcher struct {
	mu    sync.Mutex
	calls int
	dsts  []string
}

func (f *fakeFetcher) Fetch(ctx context.Context, urls []string, dst string) error {
	f.mu.Lock()
	f.calls++
	f.dsts = append(f.dsts, dst)
	f.mu.Unlock()
	return os.WriteFile(dst, []byte("data"), 0o644)
}

type fakeMuxer struct {
	calls             int
	video, audio, dst string
}

func (f *fakeMuxer) Mux(ctx context.Context, video, audio, dst string) error {
	f.calls++
	f.video, f.audio, f.dst = video, audio, dst
	return os.WriteFile(dst, []byte("mp4"), 0o644)
}

func sampleVideo() domain.Video {
	return domain.Video{
		AID: 100, BVID: "BV1", Title: "t",
		Pages: []domain.Page{
			{CID: 11, Index: 1}, {CID: 22, Index: 2},
		},
	}
}

func sampleManifest() domain.Manifest {
	return domain.Manifest{
		Videos: []domain.Stream{
			{Kind: domain.StreamVideo, Quality: domain.NewQuality(32), Codec: domain.CodecAVC, BandWidth: 100, URLs: []string{"v"}},
		},
		Audios: []domain.Stream{
			{Kind: domain.StreamAudio, BandWidth: 50, URLs: []string{"a"}},
		},
	}
}

// AC4（单元）：page=2 → 用第 2P 的 cid 请求播放地址，结果 Page.CID 一致。
func TestResolveSelectsPage(t *testing.T) {
	fs := &fakeStreams{m: sampleManifest()}
	d := NewDownloader(fakeMeta{v: sampleVideo()}, fs, &fakeFetcher{}, &fakeMuxer{}, nil)
	info, err := d.Resolve(context.Background(), "BV1", 2, 80)
	if err != nil {
		t.Fatal(err)
	}
	if info.Page.CID != 22 {
		t.Errorf("page CID = %d, want 22", info.Page.CID)
	}
	if fs.gotCID != 22 {
		t.Errorf("Manifest called with cid = %d, want 22", fs.gotCID)
	}
}

// Download 编排：下载 video+audio 两路、合并一次、返回选定清晰度与产物路径。
func TestDownloadOrchestration(t *testing.T) {
	ff := &fakeFetcher{}
	fm := &fakeMuxer{}
	d := NewDownloader(fakeMeta{v: sampleVideo()}, &fakeStreams{m: sampleManifest()}, ff, fm, nil)

	dst := t.TempDir() + "/out.mp4"
	res, err := d.Download(context.Background(), Request{BVID: "BV1", Page: 1, QN: 80, PreferCodec: domain.CodecAVC, FallbackCodec: domain.CodecHEVC}, dst)
	if err != nil {
		t.Fatal(err)
	}
	if ff.calls != 2 {
		t.Errorf("fetch calls = %d, want 2 (video+audio)", ff.calls)
	}
	if fm.calls != 1 {
		t.Errorf("mux calls = %d, want 1", fm.calls)
	}
	if res.ChosenQuality.QN != 32 {
		t.Errorf("chosen QN = %d, want 32 (downgraded from 80)", res.ChosenQuality.QN)
	}
	if res.Codec != domain.CodecAVC {
		t.Errorf("codec = %v, want AVC", res.Codec)
	}
	if res.OutputPath != dst {
		t.Errorf("output = %q, want %q", res.OutputPath, dst)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("output file missing: %v", err)
	}
}

func TestResolvePageNotFound(t *testing.T) {
	d := NewDownloader(fakeMeta{v: sampleVideo()}, &fakeStreams{m: sampleManifest()}, &fakeFetcher{}, &fakeMuxer{}, nil)
	if _, err := d.Resolve(context.Background(), "BV1", 99, 80); err == nil {
		t.Error("want error for missing page")
	}
}
