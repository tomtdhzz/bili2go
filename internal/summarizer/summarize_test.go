package summarizer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadDigest(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestFallbackParity：Go 回退五节 markdown 与现 Python 输出（golden）逐字一致（AC-6.1）。
func TestFallbackParity(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_KEY", "")
	cases := []struct{ fixture, golden string }{
		{"digest.fixture.json", "summary.fixture.golden.md"},
		{"digest.silent.json", "summary.silent.golden.md"},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			res, err := Summarize(context.Background(), loadDigest(t, c.fixture), 25)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Fallback || res.Model != "fallback" {
				t.Fatalf("want fallback, got model=%q fallback=%v", res.Model, res.Fallback)
			}
			if want := readGolden(t, c.golden); res.Markdown != want {
				t.Errorf("parity mismatch\n--- got ---\n%s\n--- want ---\n%s", res.Markdown, want)
			}
		})
	}
}

// TestValidateRejectsMissingKeys：缺必需字段 → ErrInvalid + Python 口径错误串（AC-6.2）。
func TestValidateRejectsMissingKeys(t *testing.T) {
	_, err := Summarize(context.Background(), map[string]any{"source": map[string]any{}}, 25)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "digest 缺少必需字段: ['transcript', 'screen_keywords', 'gaps']") {
		t.Errorf("unexpected message: %q", err.Error())
	}
}
