package analyze

import "testing"

func kwByTerm(kws []screenKeywordJSON, term string) (screenKeywordJSON, bool) {
	for _, k := range kws {
		if k.Term == term {
			return k, true
		}
	}
	return screenKeywordJSON{}, false
}

// TestAggregateKeywordsUnspoken：去重计数、is_chrome(常驻)、spoken 丢弃、证据帧。
func TestAggregateKeywordsUnspoken(t *testing.T) {
	frames := []frameOCR{
		{Index: 1, TS: 0, Lines: []string{"履约时效 P95 = 4.2h", "频道LOGO", "="}},
		{Index: 2, TS: 2, Lines: []string{"预算 240万", "频道LOGO"}},
		{Index: 3, TS: 4, Lines: []string{"频道LOGO"}},
		{Index: 4, TS: 6, Lines: []string{"频道LOGO", "履约时效目前存在风险"}},
	}
	transcript := "大家好 履约时效目前存在风险 需要重点关注"
	kws := aggregateKeywords(frames, transcript, "unspoken")

	// 已口述项被丢弃（unspoken 模式）
	if _, ok := kwByTerm(kws, "履约时效目前存在风险"); ok {
		t.Errorf("spoken term should be dropped in unspoken mode")
	}
	// 纯符号行 "=" 被过滤
	if _, ok := kwByTerm(kws, "="); ok {
		t.Errorf("punctuation-only line should be filtered")
	}
	// 常驻文本 → is_chrome
	if c, ok := kwByTerm(kws, "频道LOGO"); !ok || !c.IsChrome {
		t.Errorf("频道LOGO should be is_chrome (occ=4/4); got %+v ok=%v", c, ok)
	}
	// 画面独有数据项：未口述、非 chrome、带证据帧与时间戳
	k, ok := kwByTerm(kws, "履约时效 P95 = 4.2h")
	if !ok {
		t.Fatalf("expected unspoken data term present; got %+v", kws)
	}
	if k.Spoken || k.IsChrome || k.Occurrences != 1 || k.FirstTS != 0 || len(k.EvidenceFrames) != 1 {
		t.Errorf("data term fields = %+v", k)
	}
	// 降序：数据项分数高于常驻 LOGO
	if k.Score <= 0 {
		t.Errorf("score should be positive: %+v", k)
	}
}

// TestAggregateKeywordsAll：all 模式保留已口述项（标 spoken）。
func TestAggregateKeywordsAll(t *testing.T) {
	frames := []frameOCR{
		{Index: 1, TS: 0, Lines: []string{"增长复盘"}},
	}
	kws := aggregateKeywords(frames, "本季度增长复盘开始", "all")
	k, ok := kwByTerm(kws, "增长复盘")
	if !ok || !k.Spoken {
		t.Errorf("all mode should keep spoken term flagged; got %+v ok=%v", k, ok)
	}
}
