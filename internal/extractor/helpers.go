package extractor

import (
	"os"
	"path/filepath"
	"strings"
)

var sanitizeReplacer = strings.NewReplacer(
	"/", "_", "\\", "_", ":", "_", "*", "_",
	"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	"\n", " ", "\r", " ", "\t", " ",
)

func firstFailedURL(m map[string]string) string {
	for k := range m {
		return k
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	out := sanitizeReplacer.Replace(name)
	if len(out) > 200 {
		ext := filepath.Ext(out)
		out = out[:200-len(ext)] + ext
	}
	return out
}

func writeFile(dest string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, content, 0o644)
}

func sanitizeErrorBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	repl := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	return repl.Replace(s)
}
