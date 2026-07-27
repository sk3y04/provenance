// Package manifest defines the scan manifest, media items, filtering, and human-readable output.
package manifest

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Item struct {
	ID          string    `json:"id,omitempty"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	Extension   string    `json:"extension,omitempty"`
	Size        int64     `json:"size,omitempty"`
	Source      string    `json:"source,omitempty"`
	Creator     string    `json:"creator,omitempty"`
	PostID      string    `json:"post_id,omitempty"`
	PublishedAt time.Time `json:"published_at,omitempty"`
	Destination string    `json:"destination,omitempty"`
	Kind        string    `json:"kind,omitempty"`
}

type Manifest struct {
	SourceURL string    `json:"source_url"`
	Site      string    `json:"site"`
	ScannedAt time.Time `json:"scanned_at"`
	Items     []Item    `json:"items"`
}

type Summary struct {
	Count      int            `json:"count"`
	KnownSize  int64          `json:"known_size"`
	Extensions map[string]int `json:"extensions"`
}

type FilterOptions struct {
	IncludeExt  []string
	ExcludeExt  []string
	MinSize     int64
	MaxSize     int64
	TitleMatch  string
	TitleReject string
}

func New(sourceURL, site string, items []Item) Manifest {
	return Manifest{SourceURL: sourceURL, Site: site, ScannedAt: time.Now(), Items: NormalizeItems(items)}
}

func NormalizeItems(items []Item) []Item {
	out := make([]Item, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		it.URL = strings.TrimSpace(it.URL)
		if it.URL == "" {
			continue
		}
		if it.Filename == "" {
			it.Filename = filepath.Base(strings.Split(it.URL, "?")[0])
		}
		if it.Title == "" {
			it.Title = it.Filename
		}
		if it.Extension == "" {
			it.Extension = ExtOf(it.Filename)
		}
		key := it.URL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, it)
	}
	return out
}

func (m Manifest) Summary() Summary {
	s := Summary{Count: len(m.Items), Extensions: map[string]int{}}
	for _, it := range m.Items {
		if it.Size > 0 {
			s.KnownSize += it.Size
		}
		ext := it.Extension
		if ext == "" {
			ext = "unknown"
		}
		s.Extensions[ext]++
	}
	return s
}

func (m Manifest) Filter(opts FilterOptions) (Manifest, error) {
	items, err := FilterItems(m.Items, opts)
	if err != nil {
		return Manifest{}, err
	}
	m.Items = items
	return m, nil
}

func FilterItems(items []Item, opts FilterOptions) ([]Item, error) {
	include := extSet(opts.IncludeExt)
	exclude := extSet(opts.ExcludeExt)
	var titleMatch, titleReject *regexp.Regexp
	var err error
	if strings.TrimSpace(opts.TitleMatch) != "" {
		titleMatch, err = regexp.Compile(opts.TitleMatch)
		if err != nil {
			return nil, fmt.Errorf("title-match regex: %w", err)
		}
	}
	if strings.TrimSpace(opts.TitleReject) != "" {
		titleReject, err = regexp.Compile(opts.TitleReject)
		if err != nil {
			return nil, fmt.Errorf("title-exclude regex: %w", err)
		}
	}

	out := make([]Item, 0, len(items))
	for _, it := range NormalizeItems(items) {
		ext := strings.TrimPrefix(strings.ToLower(it.Extension), ".")
		if len(include) > 0 {
			if _, ok := include[ext]; !ok {
				continue
			}
		}
		if _, ok := exclude[ext]; ok {
			continue
		}
		if opts.MinSize > 0 && it.Size > 0 && it.Size < opts.MinSize {
			continue
		}
		if opts.MaxSize > 0 && it.Size > 0 && it.Size > opts.MaxSize {
			continue
		}
		text := it.Title + " " + it.Filename + " " + it.URL
		if titleMatch != nil && !titleMatch.MatchString(text) {
			continue
		}
		if titleReject != nil && titleReject.MatchString(text) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func PrintHuman(w io.Writer, m Manifest) {
	s := m.Summary()
	_, _ = fmt.Fprintf(w, "Source: %s\n", m.SourceURL)
	_, _ = fmt.Fprintf(w, "Site:   %s\n", m.Site)
	_, _ = fmt.Fprintf(w, "Items:  %d\n", s.Count)
	if s.KnownSize > 0 {
		_, _ = fmt.Fprintf(w, "Known total size: %s\n", HumanSize(s.KnownSize))
	}
	if len(s.Extensions) > 0 {
		_, _ = fmt.Fprintln(w, "\nFile types:")
		keys := make([]string, 0, len(s.Extensions))
		for k := range s.Extensions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = fmt.Fprintf(w, "  %-8s %d\n", k, s.Extensions[k])
		}
	}
	if len(m.Items) > 0 {
		_, _ = fmt.Fprintln(w, "\nLargest / first items:")
		items := append([]Item(nil), m.Items...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].Size > items[j].Size })
		limit := len(items)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			it := items[i]
			size := "unknown"
			if it.Size > 0 {
				size = HumanSize(it.Size)
			}
			name := it.Title
			if name == "" {
				name = it.Filename
			}
			_, _ = fmt.Fprintf(w, "  %2d. %-48s %10s\n", i+1, truncate(name, 48), size)
		}
	}
}

func ExtOf(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(strings.Split(name, "?")[0])), ".")
	return ext
}

func ParseCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.TrimPrefix(p, "."))
		if p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	mul := int64(1)
	units := []struct {
		suffix string
		mul    int64
	}{
		{"gb", 1024 * 1024 * 1024}, {"g", 1024 * 1024 * 1024},
		{"mb", 1024 * 1024}, {"m", 1024 * 1024},
		{"kb", 1024}, {"k", 1024}, {"b", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			mul = u.mul
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(v * float64(mul)), nil
}

func HumanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n)
	for _, u := range units {
		f /= 1024
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f PB", f/1024)
}

func extSet(exts []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, ext := range exts {
		ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if ext != "" {
			out[ext] = struct{}{}
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
