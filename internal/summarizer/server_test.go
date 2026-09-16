package summarizer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "digest.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	// 确保摘要走回退（正常路径 200）。
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	srv := httptest.NewServer(Handler{})
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

// TestSummarizeContract：/summarize 与 /health 的成功/错误码（AC-6.2）。
func TestSummarizeContract(t *testing.T) {
	srv := newServer(t)

	if resp, _ := post(t, srv.URL+"/summarize", fixtureBytes(t), nil); resp.StatusCode != 200 {
		t.Errorf("valid digest → %d, want 200", resp.StatusCode)
	}
	if resp, _ := post(t, srv.URL+"/summarize", []byte("{bad"), nil); resp.StatusCode != 400 {
		t.Errorf("bad json → %d, want 400", resp.StatusCode)
	}
	if resp, body := post(t, srv.URL+"/summarize", []byte(`{"source":{}}`), nil); resp.StatusCode != 400 {
		t.Errorf("missing fields → %d, want 400 (%s)", resp.StatusCode, body)
	}
	if resp, _ := post(t, srv.URL+"/summarize?top=abc", fixtureBytes(t), nil); resp.StatusCode != 400 {
		t.Errorf("top=abc → %d, want 400", resp.StatusCode)
	}
	// 未知路径 / 方法错配 → 404
	if resp, err := http.Get(srv.URL + "/nope"); err == nil && resp.StatusCode != 404 {
		t.Errorf("unknown path → %d, want 404", resp.StatusCode)
	}
	if resp, err := http.Get(srv.URL + "/summarize"); err == nil && resp.StatusCode != 404 {
		t.Errorf("GET /summarize → %d, want 404", resp.StatusCode)
	}
	// /health
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("/health → %d, want 200", resp.StatusCode)
	}
	var h map[string]any
	json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if h["status"] != "ok" {
		t.Errorf("/health body = %v", h)
	}
}

// TestAuth：opt-in bearer 鉴权（AC-6.3，复刻迭代5 test_app_auth.py 五例）。
func TestAuth(t *testing.T) {
	srv := newServer(t)

	t.Run("no token configured allows", func(t *testing.T) {
		if resp, _ := post(t, srv.URL+"/summarize", fixtureBytes(t), nil); resp.StatusCode != 200 {
			t.Errorf("→ %d, want 200", resp.StatusCode)
		}
	})
	t.Run("token set missing header 401", func(t *testing.T) {
		t.Setenv("SUMMARIZER_TOKEN", "s3cret")
		resp, body := post(t, srv.URL+"/summarize", fixtureBytes(t), nil)
		if resp.StatusCode != 401 {
			t.Fatalf("→ %d, want 401", resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got == "" {
			t.Errorf("missing WWW-Authenticate header")
		}
		var e map[string]any
		json.Unmarshal(body, &e)
		if e["error"] != "unauthorized" {
			t.Errorf("body = %s", body)
		}
	})
	t.Run("token set wrong 401", func(t *testing.T) {
		t.Setenv("SUMMARIZER_TOKEN", "s3cret")
		if resp, _ := post(t, srv.URL+"/summarize", fixtureBytes(t), map[string]string{"Authorization": "Bearer nope"}); resp.StatusCode != 401 {
			t.Errorf("→ %d, want 401", resp.StatusCode)
		}
	})
	t.Run("token set correct 200", func(t *testing.T) {
		t.Setenv("SUMMARIZER_TOKEN", "s3cret")
		if resp, _ := post(t, srv.URL+"/summarize", fixtureBytes(t), map[string]string{"Authorization": "Bearer s3cret"}); resp.StatusCode != 200 {
			t.Errorf("→ %d, want 200", resp.StatusCode)
		}
	})
	t.Run("health open when token set", func(t *testing.T) {
		t.Setenv("SUMMARIZER_TOKEN", "s3cret")
		resp, err := http.Get(srv.URL + "/health")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("/health → %d, want 200", resp.StatusCode)
		}
	})
}
