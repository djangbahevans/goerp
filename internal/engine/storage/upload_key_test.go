package storage

import (
	"strings"
	"testing"
)

func TestValidPurpose(t *testing.T) {
	cases := []struct {
		purpose string
		want    bool
	}{
		{"attachments", true},
		{"avatars", true},
		{"imports", true},
		{"custom_purpose-1", true},
		{"", false},
		{"../../../etc", false},
		{"a/b", false},
		{"..", false},
		{".", false},
		{strings.Repeat("a", 65), false},
		{strings.Repeat("a", 64), true},
	}

	for _, c := range cases {
		t.Run(c.purpose, func(t *testing.T) {
			if got := ValidPurpose(c.purpose); got != c.want {
				t.Errorf("ValidPurpose(%q) = %v, want %v", c.purpose, got, c.want)
			}
		})
	}
}

func TestBuildKey(t *testing.T) {
	key := BuildKey("avatars", "tenant-123", "file-456", ".png")

	if !strings.HasPrefix(key, "avatars/tenant-123/") {
		t.Errorf("BuildKey() = %q, want prefix %q", key, "avatars/tenant-123/")
	}
	if !strings.HasSuffix(key, "file-456.png") {
		t.Errorf("BuildKey() = %q, want suffix %q", key, "file-456.png")
	}
}
