package app

import (
	"context"

	"bili2go/internal/domain"
)

// MetadataSource 元数据来源（bvid → 视频与分 P）。由 internal/bilibili 实现。
type MetadataSource interface {
	View(ctx context.Context, bvid string) (domain.Video, error)
}

// StreamSource 播放地址来源（aid+cid+qn → DASH 清单）。由 internal/bilibili 实现。
type StreamSource interface {
	Manifest(ctx context.Context, aid, cid int64, qn int) (domain.Manifest, error)
}

// Fetcher 单流下载器：按 urls 顺序（主源→备源）下载到 dst。由 internal/media 实现。
type Fetcher interface {
	Fetch(ctx context.Context, urls []string, dst string) error
}

// Muxer 音视频封装器：将 video 与 audio 合并为 dst（mp4）。由 internal/media 实现。
type Muxer interface {
	Mux(ctx context.Context, videoPath, audioPath, dst string) error
}
