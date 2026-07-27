package importers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sk3y04/provenance/internal/archive"
)

var wsRE = regexp.MustCompile(`\s+`)

func ImportDocs(vaultRoot, siteURL, scope string, maxPages int, collectionName string) (*archive.Revision, error) {
	if maxPages <= 0 {
		maxPages = 100
	}

	_, err := url.Parse(siteURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	rev := newRevision(siteURL, "provenance-import-docs", "1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	visited := make(map[string]bool)
	pages := 0
	entity := archive.Entity{
		ExternalID: siteURL,
		URL:        siteURL,
		Title:      siteURL,
		Kind:       "docs_site",
		Extractor:  "docs-importer",
		CapturedAt: rev.CapturedAt,
	}

	var queue []string
	queue = append(queue, siteURL)

	hrefRE := regexp.MustCompile(`href="([^"]+)"`)
	headingRE := regexp.MustCompile(`<h[1-6][^>]*>(.*?)</h[1-6]>`)
	tagStripRE := regexp.MustCompile(`<[^>]*>`)

	for len(queue) > 0 && pages < maxPages {
		currentURL := queue[0]
		queue = queue[1:]

		if visited[currentURL] {
			continue
		}
		visited[currentURL] = true

		if scope != "" && !strings.HasPrefix(currentURL, scope) {
			continue
		}

		resp, err := client.Get(currentURL)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if err != nil {
			continue
		}

		bodyStr := string(body)
		contentType := resp.Header.Get("Content-Type")
		if !strings.Contains(contentType, "text/html") {
			continue
		}

		headings := headingRE.FindAllStringSubmatch(bodyStr, -1)
		var parts []string
		for _, h := range headings {
			if len(h) > 1 {
				parts = append(parts, "## "+tagStripRE.ReplaceAllString(h[1], ""))
			}
		}

		plainText := tagStripRE.ReplaceAllString(bodyStr, " ")
		plainText = wsRE.ReplaceAllString(plainText, " ")
		if len(plainText) > 10000 {
			plainText = safeTruncate(plainText, 10000)
		}

		docContent := strings.Join(parts, "\n")
		if docContent == "" {
			docContent = plainText
		}

		entity.Documents = append(entity.Documents, archive.Document{
			ExternalID: currentURL,
			Content:    docContent,
			Format:     archive.DocHTML,
		})

		pages++

		matches := hrefRE.FindAllStringSubmatch(bodyStr, -1)
		for _, m := range matches {
			if len(m) > 1 {
				link := m[1]
				if strings.HasPrefix(link, "#") || strings.HasPrefix(link, "javascript:") || strings.HasPrefix(link, "mailto:") {
					continue
				}
				absURL, parseErr := resolveURL(currentURL, link)
				if parseErr != nil {
					continue
				}
				if !visited[absURL] && (scope == "" || strings.HasPrefix(absURL, scope)) {
					queue = append(queue, absURL)
				}
			}
		}
	}

	rev.Entities = append(rev.Entities, entity)

	if err := persistRevision(vaultRoot, rev); err != nil {
		return nil, fmt.Errorf("write revision: %w", err)
	}

	return rev, nil
}

func resolveURL(base, href string) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(u).String(), nil
}
