package bilibili

import (
	"context"
	"net/http"
	"testing"
)

func TestNewRequestInjectsHeaders(t *testing.T) {
	c := NewClient("secret-sess")
	req, err := c.NewRequest(context.Background(), http.MethodGet,
		"https://api.bilibili.com/x/web-interface/view?bvid=BV1")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != defaultUA {
		t.Errorf("UA = %q, want %q", got, defaultUA)
	}
	if got := req.Header.Get("Referer"); got != defaultReferer {
		t.Errorf("Referer = %q, want %q", got, defaultReferer)
	}
	if got := req.Header.Get("Cookie"); got != "SESSDATA=secret-sess" {
		t.Errorf("Cookie = %q, want SESSDATA=secret-sess", got)
	}
}

func TestNewRequestNoCookieWhenEmpty(t *testing.T) {
	c := NewClient("")
	req, _ := c.NewRequest(context.Background(), http.MethodGet, "https://x")
	if got := req.Header.Get("Cookie"); got != "" {
		t.Errorf("Cookie = %q, want empty", got)
	}
}
