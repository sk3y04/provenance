package ratelimit

import (
	"testing"

	"golang.org/x/time/rate"
)

func TestNormalizeHost(t *testing.T) {
	tests := map[string]string{
		"X.COM":                "x.com",
		"www.Reddit.com:443":   "www.reddit.com",
		"i.instagram.com:8080": "i.instagram.com",
		"localhost:3000":       "localhost",
		"plain.host":           "plain.host",
		"":                     "",
	}
	for in, want := range tests {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		host      string
		wantRate  rate.Limit
		wantBurst int
	}{
		{"x.com", 2, 2},
		{"twitter.com", 2, 2},
		{"api.twitter.com", 2, 2},
		{"www.reddit.com", 1, 2},
		{"old.reddit.com", 1, 2},
		{"i.instagram.com", 1, 2},
		{"cdninstagram.com", 1, 2},
		{"www.instagram.com", 1, 2},
		{"youtube.com", 10, 10},
		{"example.com", 10, 10},
		{"unknown.local", 10, 10},
	}
	for _, tt := range tests {
		gotRate, gotBurst := classify(tt.host)
		if gotRate != tt.wantRate || gotBurst != tt.wantBurst {
			t.Errorf("classify(%q) = (%.0f, %d), want (%.0f, %d)", tt.host, gotRate, gotBurst, tt.wantRate, tt.wantBurst)
		}
	}
}

func TestGetLimiterReturnsSameInstance(t *testing.T) {
	m := New()
	l1 := m.GetLimiter("x.com")
	l2 := m.GetLimiter("x.com")
	if l1 != l2 {
		t.Error("GetLimiter returned different instances for same host")
	}
}

func TestGetLimiterDifferentHosts(t *testing.T) {
	m := New()
	l1 := m.GetLimiter("x.com")
	l2 := m.GetLimiter("reddit.com")
	if l1 == l2 {
		t.Error("GetLimiter returned same instance for different hosts")
	}
}

func TestGetLimiterNormalizesHost(t *testing.T) {
	m := New()
	l1 := m.GetLimiter("X.COM:443")
	l2 := m.GetLimiter("x.com")
	if l1 != l2 {
		t.Error("GetLimiter should normalize hosts before lookup")
	}
}
