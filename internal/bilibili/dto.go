package bilibili

// 本文件是 B 站 API 的传输层 DTO（防腐层）：仅本包可见，由适配器映射为 domain 类型，不外泄。

// API 端点。
const (
	viewURL    = "https://api.bilibili.com/x/web-interface/view"
	playurlURL = "https://api.bilibili.com/x/player/playurl"
)

// viewData 对应 x/web-interface/view 的 data。
type viewData struct {
	Aid   int64  `json:"aid"`
	Bvid  string `json:"bvid"`
	Title string `json:"title"`
	Pages []struct {
		Cid      int64  `json:"cid"`
		Page     int    `json:"page"`
		Part     string `json:"part"`
		Duration int    `json:"duration"` // 秒
	} `json:"pages"`
}

// playurlData 对应 x/player/playurl 的 data（DASH）。
type playurlData struct {
	Quality       int   `json:"quality"`
	AcceptQuality []int `json:"accept_quality"`
	Dash          *struct {
		Duration int         `json:"duration"` // 秒
		Video    []dashMedia `json:"video"`
		Audio    []dashMedia `json:"audio"`
	} `json:"dash"`
}

// dashMedia 对应 dash.video[]/audio[] 元素。B 站字段有 camelCase 与 snake_case 两套，全部捕获。
type dashMedia struct {
	ID         int      `json:"id"`
	Codecid    int      `json:"codecid"`
	Bandwidth  int      `json:"bandwidth"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	FrameRate  string   `json:"frameRate"`
	Codecs     string   `json:"codecs"`
	BaseURL    string   `json:"baseUrl"`
	BaseURLAlt string   `json:"base_url"`
	BackupURL  []string `json:"backupUrl"`
	BackupAlt  []string `json:"backup_url"`
}
