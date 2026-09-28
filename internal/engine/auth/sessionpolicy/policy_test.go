package sessionpolicy

import (
	"errors"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	valid := []Policy{
		{},
		{IdleTimeout: MinIdleTimeout},
		{AbsoluteMax: MaxAbsoluteMax},
		{IdleTimeout: time.Hour, AbsoluteMax: time.Hour},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v.Validate() = %v, want nil", p, err)
		}
	}
	invalid := []Policy{
		{IdleTimeout: 10 * time.Minute},
		{IdleTimeout: 20*time.Minute + time.Second},
		{AbsoluteMax: 30 * time.Minute},
		{AbsoluteMax: MaxAbsoluteMax + time.Minute},
		{IdleTimeout: 2 * time.Hour, AbsoluteMax: time.Hour},
	}
	for _, p := range invalid {
		if err := p.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v.Validate() = %v, want ErrInvalid", p, err)
		}
	}
}

func TestExpiresAt(t *testing.T) {
	login := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	now := login.Add(3 * time.Hour)
	ttl := 12 * time.Hour

	cases := []struct {
		policy Policy
		want   time.Time
	}{
		{Policy{}, now.Add(ttl)},
		{Policy{IdleTimeout: time.Hour}, now.Add(time.Hour)},
		{Policy{AbsoluteMax: 4 * time.Hour}, login.Add(4 * time.Hour)},
		{Policy{IdleTimeout: 2 * time.Hour, AbsoluteMax: 4 * time.Hour}, login.Add(4 * time.Hour)},
		{Policy{IdleTimeout: 30 * 24 * time.Hour}, now.Add(ttl)},
	}
	for _, c := range cases {
		if got := c.policy.ExpiresAt(now, login, ttl); !got.Equal(c.want) {
			t.Errorf("%+v.ExpiresAt() = %v, want %v", c.policy, got, c.want)
		}
	}
}
