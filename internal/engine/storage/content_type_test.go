package storage

import "testing"

func TestContentTypeAllowed(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		allowed     []string
		blocked     []string
		wantAllowed bool
	}{
		{"empty allowed and blocked permits anything", "image/png", nil, nil, true},
		{"blocked wins even with empty allowed", "application/x-sh", nil, []string{"application/x-sh"}, false},
		{"allowed list permits a listed type", "image/png", []string{"image/png", "image/jpeg"}, nil, true},
		{"allowed list rejects an unlisted type", "application/pdf", []string{"image/png", "image/jpeg"}, nil, false},
		{"blocked wins even if also in allowed", "image/png", []string{"image/png"}, []string{"image/png"}, false},
		{"case-insensitive match", "IMAGE/PNG", []string{"image/png"}, nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ContentTypeAllowed(c.contentType, c.allowed, c.blocked)
			if got != c.wantAllowed {
				t.Errorf("ContentTypeAllowed(%q, %v, %v) = %v, want %v", c.contentType, c.allowed, c.blocked, got, c.wantAllowed)
			}
		})
	}
}
