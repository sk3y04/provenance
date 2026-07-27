package manifest

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"":      0,
		"10":    10,
		"1KB":   1024,
		"1.5MB": 1572864,
		"2gb":   2 * 1024 * 1024 * 1024,
	}
	for input, want := range cases {
		got, err := ParseSize(input)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseSize(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestFilterItems(t *testing.T) {
	items := []Item{
		{URL: "https://x/a.mp4", Title: "bonus video", Filename: "a.mp4", Size: 100},
		{URL: "https://x/b.zip", Title: "archive", Filename: "b.zip", Size: 5000},
		{URL: "https://x/c.jpg", Title: "preview", Filename: "c.jpg", Size: 50},
	}
	filtered, err := FilterItems(items, FilterOptions{
		IncludeExt:  []string{"mp4", "zip"},
		MaxSize:     1000,
		TitleMatch:  "bonus|archive",
		TitleReject: "preview",
	})
	if err != nil {
		t.Fatalf("FilterItems: %v", err)
	}
	if len(filtered) != 1 || filtered[0].URL != "https://x/a.mp4" {
		t.Fatalf("filtered = %+v, want only mp4", filtered)
	}
}

func TestSummary(t *testing.T) {
	m := New("src", "test", []Item{{URL: "u1", Filename: "a.mp4", Size: 10}, {URL: "u2", Filename: "b.jpg", Size: 20}})
	s := m.Summary()
	if s.Count != 2 || s.KnownSize != 30 || s.Extensions["mp4"] != 1 || s.Extensions["jpg"] != 1 {
		t.Fatalf("summary = %+v", s)
	}
}
