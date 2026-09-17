package analyze

import (
	"encoding/json"
	"testing"
)

const whisperFixture = `{"transcription":[
  {"offsets":{"from":0,"to":13800},"text":" 大家好，下面开始复盘"},
  {"offsets":{"from":13800,"to":20580},"text":"   "},
  {"offsets":{"from":26580,"to":32460},"text":"最后介绍路线图。"},
  {"offsets":{"from":32460,"to":33000},"text":"[BLANK_AUDIO]"}
]}`

const probeAV = `{"format":{"duration":"39.000000"},"streams":[
  {"codec_type":"video","width":1280,"height":720},
  {"codec_type":"audio"}
]}`

const probeSilent = `{"format":{"duration":"10.5"},"streams":[
  {"codec_type":"video","width":640,"height":480}
]}`

func TestParseWhisperJSON(t *testing.T) {
	segs, err := parseWhisperJSON([]byte(whisperFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 { // 空白段 + [BLANK_AUDIO] 被跳过
		t.Fatalf("segments = %d, want 2 (%+v)", len(segs), segs)
	}
	if segs[0].StartS != 0 || segs[0].Text != "大家好，下面开始复盘" {
		t.Errorf("seg0 = %+v", segs[0])
	}
	if segs[1].StartS != 26.58 || segs[1].EndS != 32.46 {
		t.Errorf("seg1 times = %+v", segs[1])
	}
}

func TestParseProbe(t *testing.T) {
	av, err := parseProbe([]byte(probeAV))
	if err != nil {
		t.Fatal(err)
	}
	if !av.HasAudio || av.DurationS != 39 || av.Width != 1280 || av.Height != 720 {
		t.Errorf("av = %+v", av)
	}
	sil, err := parseProbe([]byte(probeSilent))
	if err != nil {
		t.Fatal(err)
	}
	if sil.HasAudio || sil.DurationS != 10.5 || sil.Width != 640 {
		t.Errorf("silent = %+v", sil)
	}
}

// TestAssembleDigestSchema：产出满足摘要消费方 schema（4 必需键 + 空数组非 null）。
func TestAssembleDigestSchema(t *testing.T) {
	segs, _ := parseWhisperJSON([]byte(whisperFixture))
	src, _ := parseProbe([]byte(probeAV))
	dj := assembleDigest("/tmp/x.mp4", src, "whisper", "zh", segs)
	raw, err := json.Marshal(dj)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "transcript", "screen_keywords", "gaps"} {
		if _, ok := m[k]; !ok {
			t.Errorf("digest 缺必需键 %q", k)
		}
	}
	if string(m["screen_keywords"]) != "[]" || string(m["gaps"]) != "[]" {
		t.Errorf("空数组应为 [] 非 null: kw=%s gaps=%s", m["screen_keywords"], m["gaps"])
	}
	// parseMeta（既有）应能读出 engine 与段数。
	meta, err := parseMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Engine != "whisper" || meta.Segments != 2 {
		t.Errorf("meta = %+v", meta)
	}
}

// TestAssembleDigestNoAudio：无音轨 → engine none、segments 空、仍合规。
func TestAssembleDigestNoAudio(t *testing.T) {
	src, _ := parseProbe([]byte(probeSilent))
	dj := assembleDigest("/tmp/x.mp4", src, "none", "zh", nil)
	raw, _ := json.Marshal(dj)
	meta, err := parseMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Engine != "none" || meta.Segments != 0 {
		t.Errorf("meta = %+v", meta)
	}
}
