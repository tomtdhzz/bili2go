package bilibili

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"bili2go/internal/domain"
)

const playurlJSON = `{"code":0,"data":{"quality":32,"accept_quality":[112,80,32,16],"dash":{"duration":1494,"video":[{"id":32,"codecid":7,"bandwidth":115367,"baseUrl":"http://cdn/v32avc","backupUrl":["http://cdn/b1","http://cdn/b2"]},{"id":16,"codecid":12,"bandwidth":78629,"base_url":"https://cdn/v16hevc","backup_url":["https://cdn/b3"]}],"audio":[{"id":30232,"codecid":0,"bandwidth":130970,"baseUrl":"http://cdn/a1"}]}}}`

func TestPlayURLManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(playurlJSON))
	}))
	defer srv.Close()

	p := NewPlayURL(NewClient(""))
	p.base = srv.URL

	man, err := p.Manifest(context.Background(), 100, 11, 120)
	if err != nil {
		t.Fatalf("Manifest error: %v", err)
	}

	wantAvail := []domain.Quality{domain.NewQuality(32), domain.NewQuality(16)}
	if !reflect.DeepEqual(man.Available, wantAvail) {
		t.Fatalf("Available = %+v, want %+v", man.Available, wantAvail)
	}

	if len(man.Videos) != 2 {
		t.Fatalf("Videos len = %d, want 2", len(man.Videos))
	}
	v0 := man.Videos[0]
	wantURLs := []string{"https://cdn/v32avc", "https://cdn/b1", "https://cdn/b2"}
	if !reflect.DeepEqual(v0.URLs, wantURLs) {
		t.Fatalf("v0.URLs = %v, want %v", v0.URLs, wantURLs)
	}
	if v0.Codec != domain.CodecAVC {
		t.Fatalf("v0.Codec = %v, want AVC", v0.Codec)
	}
	if v0.Kind != domain.StreamVideo {
		t.Fatalf("v0.Kind = %v, want video", v0.Kind)
	}
	if v0.Quality != domain.NewQuality(32) {
		t.Fatalf("v0.Quality = %+v", v0.Quality)
	}
	if v0.BandWidth != 115367 {
		t.Fatalf("v0.BandWidth = %d", v0.BandWidth)
	}
	if v0.Duration != 1494*time.Second {
		t.Fatalf("v0.Duration = %v", v0.Duration)
	}

	v1 := man.Videos[1]
	wantURLs1 := []string{"https://cdn/v16hevc", "https://cdn/b3"}
	if !reflect.DeepEqual(v1.URLs, wantURLs1) {
		t.Fatalf("v1.URLs = %v, want %v", v1.URLs, wantURLs1)
	}
	if v1.Codec != domain.CodecHEVC {
		t.Fatalf("v1.Codec = %v, want HEVC", v1.Codec)
	}

	if len(man.Audios) != 1 {
		t.Fatalf("Audios len = %d, want 1", len(man.Audios))
	}
	a0 := man.Audios[0]
	if a0.Kind != domain.StreamAudio {
		t.Fatalf("a0.Kind = %v, want audio", a0.Kind)
	}
	if !reflect.DeepEqual(a0.URLs, []string{"https://cdn/a1"}) {
		t.Fatalf("a0.URLs = %v", a0.URLs)
	}
	if a0.BandWidth != 130970 {
		t.Fatalf("a0.BandWidth = %d", a0.BandWidth)
	}
	if a0.Duration != 1494*time.Second {
		t.Fatalf("a0.Duration = %v", a0.Duration)
	}
}

func TestPlayURLNoDash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"quality":32,"accept_quality":[32]}}`))
	}))
	defer srv.Close()

	p := NewPlayURL(NewClient(""))
	p.base = srv.URL

	_, err := p.Manifest(context.Background(), 100, 11, 120)
	if err == nil {
		t.Fatalf("expected error when dash is nil")
	}
}
