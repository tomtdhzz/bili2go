package domain

import "time"

// StreamKind 流类型。
type StreamKind int

const (
	StreamVideo StreamKind = iota
	StreamAudio
)

// Stream 一路视频或音频流（值对象）。
type Stream struct {
	Kind      StreamKind
	Quality   Quality // 仅 video 有意义
	Codec     Codec   // 仅 video 有意义
	BandWidth int
	URLs      []string // 主源 + 备源，已归一 https
	Duration  time.Duration
}

// Manifest 一个 cid 在某次 playurl 下的可选流集合。
// 关键：Available 取自实际存在的 video 流 id，而非上游 accept_quality（见 tech-design §5.6）。
type Manifest struct {
	Available []Quality
	Videos    []Stream
	Audios    []Stream
}
