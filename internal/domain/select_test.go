package domain

import "testing"

func vid(qn int, c Codec, bw int) Stream {
	return Stream{Kind: StreamVideo, Quality: NewQuality(qn), Codec: c, BandWidth: bw, URLs: []string{"u"}}
}

// manifest 复刻实测 BV1NKNv69EZz（匿名）：实际只有 480P(32) 与 360P(16)，各 3 编码。
func manifest() Manifest {
	return Manifest{
		Videos: []Stream{
			vid(32, CodecAVC, 115367), vid(32, CodecHEVC, 116591), vid(32, CodecAV1, 94673),
			vid(16, CodecHEVC, 78629), vid(16, CodecAVC, 74988), vid(16, CodecAV1, 62078),
		},
		Audios: []Stream{
			{Kind: StreamAudio, BandWidth: 66154, URLs: []string{"a1"}},
			{Kind: StreamAudio, BandWidth: 130970, URLs: []string{"a2"}},
		},
	}
}

// AC3：请求 1080P(80) 不存在 → 降到实际最高 480P(32)，而非 accept_quality。
func TestSelectDowngradeToPresentQN(t *testing.T) {
	v, a, q, err := SelectStreams(manifest(), 80, CodecAVC, CodecHEVC)
	if err != nil {
		t.Fatal(err)
	}
	if q.QN != 32 {
		t.Errorf("chosen QN = %d, want 32", q.QN)
	}
	if v.Codec != CodecAVC {
		t.Errorf("codec = %v, want AVC", v.Codec)
	}
	if a.BandWidth != 130970 {
		t.Errorf("audio bw = %d, want highest 130970", a.BandWidth)
	}
}

func TestSelectPreferCodec(t *testing.T) {
	v, _, _, _ := SelectStreams(manifest(), 32, CodecHEVC, CodecAVC)
	if v.Codec != CodecHEVC {
		t.Errorf("codec = %v, want HEVC", v.Codec)
	}
}

// AC5：某清晰度无 prefer 编码 → 回退，并在同编码取最高码率。
func TestSelectCodecFallback(t *testing.T) {
	m := Manifest{
		Videos: []Stream{vid(32, CodecAVC, 100), vid(32, CodecAVC, 200)},
		Audios: []Stream{{Kind: StreamAudio, BandWidth: 1, URLs: []string{"a"}}},
	}
	v, _, _, err := SelectStreams(m, 32, CodecAV1, CodecAVC)
	if err != nil {
		t.Fatal(err)
	}
	if v.Codec != CodecAVC {
		t.Errorf("codec = %v, want AVC(fallback)", v.Codec)
	}
	if v.BandWidth != 200 {
		t.Errorf("bw = %d, want highest 200", v.BandWidth)
	}
}

func TestSelectDefaultQNTakesHighest(t *testing.T) {
	_, _, q, _ := SelectStreams(manifest(), 0, CodecAVC, CodecHEVC)
	if q.QN != 32 {
		t.Errorf("default chosen QN = %d, want highest present 32", q.QN)
	}
}

func TestSelectNoStreams(t *testing.T) {
	if _, _, _, err := SelectStreams(Manifest{}, 80, CodecAVC, CodecAVC); err == nil {
		t.Error("want error on empty manifest")
	}
}
