package domain

import (
	"fmt"
	"strings"
)

// Codec 视频编码（领域枚举），映射自 B 站 codecid。
type Codec int

const (
	CodecUnknown Codec = iota
	CodecAVC           // H.264 (codecid 7)
	CodecHEVC          // H.265 (codecid 12)
	CodecAV1           // AV1   (codecid 13)
)

// String 返回编码的可读名。
func (c Codec) String() string {
	switch c {
	case CodecAVC:
		return "AVC"
	case CodecHEVC:
		return "HEVC"
	case CodecAV1:
		return "AV1"
	default:
		return "Unknown"
	}
}

// CodecFromID 由 B 站 codecid 映射到领域编码。
func CodecFromID(codecid int) Codec {
	switch codecid {
	case 7:
		return CodecAVC
	case 12:
		return CodecHEVC
	case 13:
		return CodecAV1
	default:
		return CodecUnknown
	}
}

// ParseCodec 解析 CLI/HTTP 传入的编码名（avc/hevc/av1）；空串默认 AVC。
func ParseCodec(s string) (Codec, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "avc", "h264", "h.264":
		return CodecAVC, true
	case "hevc", "h265", "h.265":
		return CodecHEVC, true
	case "av1":
		return CodecAV1, true
	default:
		return CodecUnknown, false
	}
}

// FnvalDash playurl 功能位掩码：启用 DASH + 4K + HDR + 杜比 + 8K + AV1。
const FnvalDash = 4048

// Quality 清晰度（值对象）。
type Quality struct {
	QN   int    `json:"qn"`
	Name string `json:"name"`
}

var qualityNames = map[int]string{
	16: "360P", 32: "480P", 64: "720P", 74: "720P60",
	80: "1080P", 112: "1080P+", 116: "1080P60",
	120: "4K", 125: "HDR", 126: "杜比视界", 127: "8K",
}

// QualityName 返回 qn 对应的清晰度名，未知时回退为 "QN<qn>"。
func QualityName(qn int) string {
	if n, ok := qualityNames[qn]; ok {
		return n
	}
	return fmt.Sprintf("QN%d", qn)
}

// NewQuality 由 qn 构造 Quality。
func NewQuality(qn int) Quality { return Quality{QN: qn, Name: QualityName(qn)} }
