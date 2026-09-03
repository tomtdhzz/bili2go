package bilibili

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"bili2go/internal/app"
	"bili2go/internal/domain"
)

// Metadata 实现 app.MetadataSource：bvid → 视频与分 P。
type Metadata struct {
	c    *Client
	base string
}

var _ app.MetadataSource = (*Metadata)(nil)

// NewMetadata 构造元数据适配器。
func NewMetadata(c *Client) *Metadata {
	return &Metadata{c: c, base: viewURL}
}

// extractBVID 从裸 id 或含 /video/BVxxxx 的完整 URL 中提取 BV 号。
func extractBVID(s string) (string, error) {
	i := 0
	for i < len(s) {
		if s[i] == 'B' && i+1 < len(s) && s[i+1] == 'V' {
			j := i + 2
			for j < len(s) && isBVIDChar(s[j]) {
				j++
			}
			// "BV" + 至少一个合法字符才视为有效 id。
			if j > i+2 {
				return s[i:j], nil
			}
		}
		i++
	}
	return "", fmt.Errorf("no bvid found in %q", s)
}

func isBVIDChar(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// View 拉取视频元数据并映射为 domain.Video。
func (m *Metadata) View(ctx context.Context, bvid string) (domain.Video, error) {
	id, err := extractBVID(bvid)
	if err != nil {
		return domain.Video{}, err
	}
	u := BuildURL(m.base, url.Values{"bvid": {id}})

	var data viewData
	if err := m.c.GetJSON(ctx, u, &data); err != nil {
		return domain.Video{}, err
	}

	v := domain.Video{
		AID:   data.Aid,
		BVID:  data.Bvid,
		Title: data.Title,
		Pages: make([]domain.Page, 0, len(data.Pages)),
	}
	for _, p := range data.Pages {
		v.Pages = append(v.Pages, domain.Page{
			CID:      p.Cid,
			Index:    p.Page,
			Title:    p.Part,
			Duration: time.Duration(p.Duration) * time.Second,
		})
	}
	return v, nil
}
