package diagnose

import (
	"errors"
	"strings"
	"testing"
)

func TestHint(t *testing.T) {
	cases := []struct {
		err  string
		want string
	}{
		{"HTTP Error 403: Forbidden", "cookies"},
		{"HTTP Error 429: Too Many Requests", "concurrency"},
		{"no space left on device", "disk"},
		{"unsupported url", "supported"},
	}
	for _, tc := range cases {
		got := Hint(errors.New(tc.err))
		if !strings.Contains(strings.ToLower(got), strings.ToLower(tc.want)) {
			t.Fatalf("Hint(%q) = %q, want substring %q", tc.err, got, tc.want)
		}
	}
}
