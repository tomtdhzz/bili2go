package domain

import "sort"

// SelectStreams 依据期望 qn 与编码，从 Manifest 选出一路视频流与一路音频流。
// 选择基于「实际存在的流」：可用清晰度取自 Videos 的 id 集合，而非上游 accept_quality。
// 规则见 tech-design §5.6。
func SelectStreams(m Manifest, qn int, prefer, fallback Codec) (video, audio Stream, chosen Quality, err error) {
	if len(m.Videos) == 0 {
		return Stream{}, Stream{}, Quality{}, ErrNoStreams
	}
	if len(m.Audios) == 0 {
		return Stream{}, Stream{}, Quality{}, ErrNoAudio
	}
	chosenQN := chooseQN(m.Videos, qn)
	video, err = pickVideo(m.Videos, chosenQN, prefer, fallback)
	if err != nil {
		return Stream{}, Stream{}, Quality{}, err
	}
	audio = pickAudio(m.Audios)
	return video, audio, video.Quality, nil
}

// chooseQN 选定目标清晰度：精确命中优先；否则取 <=qn 的最高；qn<=0 或全部高于 qn 时取最高可用。
func chooseQN(videos []Stream, qn int) int {
	present := map[int]struct{}{}
	var ids []int
	for _, v := range videos {
		if _, ok := present[v.Quality.QN]; !ok {
			present[v.Quality.QN] = struct{}{}
			ids = append(ids, v.Quality.QN)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids))) // 降序
	if qn <= 0 {
		return ids[0]
	}
	if _, ok := present[qn]; ok {
		return qn
	}
	for _, id := range ids { // 降序，第一个 <= qn 即最高可用降级
		if id <= qn {
			return id
		}
	}
	return ids[len(ids)-1] // 全部高于 qn：取最低可用
}

// pickVideo 在指定清晰度的视频流中，按 prefer→fallback→任意 选编码，同编码取最高码率。
func pickVideo(videos []Stream, qn int, prefer, fallback Codec) (Stream, error) {
	var pool []Stream
	for _, v := range videos {
		if v.Quality.QN == qn {
			pool = append(pool, v)
		}
	}
	if len(pool) == 0 {
		return Stream{}, ErrQualityUnavailable
	}
	for _, codec := range []Codec{prefer, fallback} {
		if codec == CodecUnknown {
			continue
		}
		if s, ok := bestByBandwidth(pool, codec); ok {
			return s, nil
		}
	}
	s, _ := bestByBandwidth(pool, CodecUnknown) // 不限编码
	return s, nil
}

// bestByBandwidth 在 pool 中取指定编码（CodecUnknown=不限）里 bandwidth 最高的一路。
func bestByBandwidth(pool []Stream, codec Codec) (Stream, bool) {
	var best Stream
	found := false
	for _, s := range pool {
		if codec != CodecUnknown && s.Codec != codec {
			continue
		}
		if !found || s.BandWidth > best.BandWidth {
			best = s
			found = true
		}
	}
	return best, found
}

// pickAudio 取音频流中码率最高的一路。
func pickAudio(audios []Stream) Stream {
	best := audios[0]
	for _, a := range audios[1:] {
		if a.BandWidth > best.BandWidth {
			best = a
		}
	}
	return best
}
