package ipallowlist

import "testing"

func TestParse_CanonicalizesEntries(t *testing.T) {
	got, err := Parse(" 10.1.2.3/8 ,203.0.113.7,, 2001:db8::1/32, 10.0.0.0/8")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if s := Format(got); s != "10.0.0.0/8,203.0.113.7/32,2001:db8::/32" {
		t.Errorf("Format(Parse()) = %q", s)
	}
}

func TestParse_UnmapsIPv4MappedEntries(t *testing.T) {
	got, err := Parse("::ffff:203.0.113.5, ::ffff:198.51.100.0/120")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if s := Format(got); s != "203.0.113.5/32,198.51.100.0/24" {
		t.Errorf("Format(Parse()) = %q, want the IPv4 forms", s)
	}
	if !Allows(got, "::ffff:203.0.113.5") || !Allows(got, "198.51.100.9") {
		t.Error("an unmapped entry must admit both the mapped and plain client address")
	}
}

func TestParse_Empty(t *testing.T) {
	got, err := Parse("  ")
	if err != nil || len(got) != 0 {
		t.Fatalf("Parse(blank) = %v, %v, want an empty list", got, err)
	}
}

func TestParse_RejectsGarbage(t *testing.T) {
	for _, in := range []string{"10.0.0.0/33", "example.com", "10.0.0.0/8, nope", "fe80::1%eth0"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) error = nil, want one", in)
		}
	}
}

func TestAllows(t *testing.T) {
	list, err := Parse("203.0.113.0/24,2001:db8::/32")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	cases := map[string]bool{
		"203.0.113.9":        true,
		"::ffff:203.0.113.9": true,
		"2001:db8::5":        true,
		"198.51.100.1":       false,
		"not-an-ip":          false,
	}
	for ip, want := range cases {
		if got := Allows(list, ip); got != want {
			t.Errorf("Allows(%q) = %v, want %v", ip, got, want)
		}
	}
	if !Allows(nil, "198.51.100.1") {
		t.Error("an empty list must allow every address")
	}
}
