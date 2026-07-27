package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func trim(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func findCookieFiles() []string {
	var found []string
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}

	visit := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") {
				if path != wd {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := strings.ToLower(info.Name())
		if strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".json") {
			if strings.Contains(name, "cookies") || strings.Contains(name, "cookie") {
				rel, _ := filepath.Rel(wd, path)
				found = append(found, rel)
			}
		}
		return nil
	}

	_ = filepath.Walk(wd, visit)
	return found
}
