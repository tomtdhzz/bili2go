package domain

import "errors"

var (
	// ErrNoStreams 无可用 DASH 视频流。
	ErrNoStreams = errors.New("no dash video streams available")
	// ErrNoAudio 无可用音频流。
	ErrNoAudio = errors.New("no dash audio streams available")
	// ErrQualityUnavailable 选定清晰度下无视频流。
	ErrQualityUnavailable = errors.New("requested quality unavailable")
	// ErrPageNotFound 指定分 P 不存在。
	ErrPageNotFound = errors.New("page not found")
)
