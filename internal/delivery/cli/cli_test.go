package cli

import "testing"

func TestRunNoArgs(t *testing.T) {
	if code := Run(nil); code != 2 {
		t.Errorf("Run(nil) = %d, want 2 (usage)", code)
	}
}

func TestRunUnknownSubcommand(t *testing.T) {
	if code := Run([]string{"bogus"}); code != 2 {
		t.Errorf("Run(bogus) = %d, want 2", code)
	}
}

func TestInfoMissingBVID(t *testing.T) {
	if code := Run([]string{"info"}); code != 2 {
		t.Errorf("Run(info) without -bvid = %d, want 2", code)
	}
}

func TestDownloadMissingOutput(t *testing.T) {
	if code := Run([]string{"dl", "-bvid", "BV1"}); code != 2 {
		t.Errorf("Run(dl) without -o = %d, want 2", code)
	}
}
