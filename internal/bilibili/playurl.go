package bilibili

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"bili2go/internal/app"
	"bili2go/internal/domain"
)

// PlayURL 实现 app.StreamSource：aid+cid+qn → DASH 清单。
type PlayURL struct {
	c    *Client
	base string
}

var _ app.StreamSource = (*PlayURL)(nil)

// NewPlayURL 构造播放地址适配器。
func NewPlayURL(c *Client) *PlayURL {
	return &PlayURL{c: c, base: playurlURL}
}

// Manifest 拉取 DASH 播放清单并映射为 domain.Manifest。
func (p *PlayURL) Manifest(ctx context.Context, aid, cid int64, qn int) (domain.Manifest, error) {
	u := BuildURL(p.base, url.Values{
		"avid":  {strconv.FormatInt(aid, 10)},
		"cid":   {strconv.FormatInt(cid, 10)},
		"qn":    {strconv.Itoa(qn)},
		"fnval": {strconv.Itoa(domain.FnvalDash)},
		"fnver": {"0"},
		"fourk": {"1"},
		"otype": {"json"},
	})

	var data playurlData
	if err := p.c.GetJSON(ctx, u, &data); err != nil {
		return domain.Manifest{}, err
	}
	if data.Dash == nil {
		return domain.Manifest{}, domain.ErrNoStreams
	}

	dur := time.Duration(data.Dash.Duration) * time.Second

	man := domain.Manifest{
		Videos: make([]domain.Stream, 0, len(data.Dash.Video)),
		Audios: make([]domain.Stream, 0, len(data.Dash.Audio)),
	}

	seen := make(map[int]struct{})
	ids := make([]int, 0, len(data.Dash.Video))
	for _, v := range data.Dash.Video {
		man.Videos = append(man.Videos, domain.Stream{
			Kind:      domain.StreamVideo,
			Quality:   domain.NewQuality(v.ID),
			Codec:     domain.CodecFromID(v.Codecid),
			BandWidth: v.Bandwidth,
			URLs:      mediaURLs(v),
			Duration:  dur,
		})
		if _, ok := seen[v.ID]; !ok {
			seen[v.ID] = struct{}{}
			ids = append(ids, v.ID)
		}
	}

	// Available：取自实际 video 流 id 去重，降序（非 accept_quality）。
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	man.Available = make([]domain.Quality, 0, len(ids))
	for _, id := range ids {
		man.Available = append(man.Available, domain.NewQuality(id))
	}

	for _, a := range data.Dash.Audio {
		man.Audios = append(man.Audios, domain.Stream{
			Kind:      domain.StreamAudio,
			BandWidth: a.Bandwidth,
			URLs:      mediaURLs(a),
			Duration:  dur,
		})
	}

	return man, nil
}

// mediaURLs 归集主源(优先 baseUrl，否则 base_url) + 全部备源(backupUrl ++ backup_url)，
// 逐个把 http 归一为 https。
func mediaURLs(m dashMedia) []string {
	primary := m.BaseURL
	if primary == "" {
		primary = m.BaseURLAlt
	}

	raw := make([]string, 0, 1+len(m.BackupURL)+len(m.BackupAlt))
	if primary != "" {
		raw = append(raw, primary)
	}
	raw = append(raw, m.BackupURL...)
	raw = append(raw, m.BackupAlt...)

	out := make([]string, len(raw))
	for i, u := range raw {
		out[i] = httpsURL(u)
	}
	return out
}

// httpsURL 将 http:// 前缀归一为 https://。
func httpsURL(u string) string {
	if strings.HasPrefix(u, "http://") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}
