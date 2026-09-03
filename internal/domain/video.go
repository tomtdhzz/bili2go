package domain

import "time"

// Page 视频分 P（值对象）。
type Page struct {
	CID      int64
	Index    int // 1-based
	Title    string
	Duration time.Duration
}

// Video 视频聚合。
type Video struct {
	AID   int64
	BVID  string
	Title string
	Pages []Page
}

// PageByIndex 返回 1-based 分 P；page<=0 视为第 1P。
func (v Video) PageByIndex(page int) (Page, bool) {
	if page <= 0 {
		page = 1
	}
	for _, p := range v.Pages {
		if p.Index == page {
			return p, true
		}
	}
	return Page{}, false
}
