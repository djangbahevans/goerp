package abi

import (
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestHTTPFetchInput_RedirectPresence(t *testing.T) {
	for _, follow := range []*bool{nil, new(false), new(true)} {
		raw, err := msgpack.Marshal(HTTPFetchInput{URL: "https://example.com/", TimeoutMs: 1250, FollowRedirects: follow})
		if err != nil {
			t.Fatal(err)
		}

		var wire map[string]any
		if err := msgpack.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}

		value, present := wire["follow_redirects"]
		if (follow != nil) != present || (present && value != *follow) {
			t.Fatalf("follow_redirects = %v (present=%v), input=%v", value, present, follow)
		}

		var out HTTPFetchInput
		if err := msgpack.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}

		if out.TimeoutMs != 1250 || (out.FollowRedirects == nil) != (follow == nil) || (follow != nil && *out.FollowRedirects != *follow) {
			t.Fatalf("decoded input = %+v", out)
		}
	}
}
