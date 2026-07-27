package importers

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/blobstore"
)

func ImportWeb(vaultRoot, seedURL, scope string, maxPages int, screenshot bool, collectionName, chromePath string) (*archive.Revision, error) {
	if maxPages <= 0 {
		maxPages = 10
	}

	_, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	rev := newRevision(seedURL, "provenance-import-web", "1.0")
	bs := blobstore.New(vaultRoot)

	if chromePath == "" {
		chromePath = findChrome()
	}

	allocOpts := []func(*chromedp.ExecAllocator){
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1920, 1080),
	}
	if chromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(chromePath))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	visited := make(map[string]bool)
	visited[seedURL] = true
	queue := []string{seedURL}
	pages := 0

	for len(queue) > 0 && pages < maxPages {
		currentURL := queue[0]
		queue = queue[1:]

		pageData, err := capturePage(browserCtx, currentURL, screenshot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[provenance] capture failed for %s: %v\n", currentURL, err)
			rev.Entities = append(rev.Entities, archive.Entity{
				ExternalID: currentURL,
				URL:        currentURL,
				Kind:       "web_page",
				Extractor:  "chromedp",
				CapturedAt: rev.CapturedAt,
			})
			continue
		}

		tmpDir, err := os.MkdirTemp("", "provenance-web-")
		if err != nil {
			continue
		}
		htmlPath := filepath.Join(tmpDir, "page.html")
		if err := os.WriteFile(htmlPath, []byte(pageData.HTML), 0o644); err != nil {
			_ = os.RemoveAll(tmpDir)
			continue
		}

		blobHash, _, err := storeFile(bs, htmlPath)
		_ = os.RemoveAll(tmpDir)
		if err != nil {
			continue
		}

		ent := archive.Entity{
			ExternalID: currentURL,
			URL:        currentURL,
			Title:      pageData.Title,
			Kind:       "web_page",
			Extractor:  "chromedp",
			CapturedAt: rev.CapturedAt,
			Artifacts:  []string{blobHash},
		}

		if pageData.Text != "" {
			ent.Text = &archive.Document{
				ExternalID: currentURL + "#text",
				Content:    pageData.Text,
				Format:     archive.DocPlain,
			}
		}

		rev.Artifacts = append(rev.Artifacts, archive.Artifact{
			Sha256: blobHash,
			Path:   "page.html",
			Size:   int64(len(pageData.HTML)),
			Kind:   archive.ArtifactHTML,
		})

		rev.Entities = append(rev.Entities, ent)
		pages++

		links, _ := discoverLinks(browserCtx, currentURL)
		for _, link := range links {
			if visited[link] {
				continue
			}
			if scope != "" && !strings.HasPrefix(link, scope) {
				continue
			}
			visited[link] = true
			queue = append(queue, link)
		}
	}

	if err := persistRevision(vaultRoot, rev); err != nil {
		return nil, fmt.Errorf("write revision: %w", err)
	}

	return rev, nil
}

type pageData struct {
	Title string
	HTML  string
	Text  string
}

func capturePage(ctx context.Context, pageURL string, screenshot bool) (*pageData, error) {
	tabCtx, cancelTab := chromedp.NewContext(ctx)
	defer cancelTab()

	timeoutCtx, cancelTimeout := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancelTimeout()

	var title, html, text string

	actions := []chromedp.Action{
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(3 * time.Second),
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html),
		chromedp.JavascriptAttribute("document.body", "innerText", &text, chromedp.ByJSPath),
	}

	if err := chromedp.Run(timeoutCtx, actions...); err != nil {
		return nil, fmt.Errorf("capture %s: %w", pageURL, err)
	}

	text = strings.TrimSpace(text)
	if len(text) > 50000 {
		text = text[:50000]
	}

	return &pageData{Title: title, HTML: html, Text: text}, nil
}

func discoverLinks(ctx context.Context, pageURL string) ([]string, error) {
	tabCtx, cancelTab := chromedp.NewContext(ctx)
	defer cancelTab()

	timeoutCtx, cancelTimeout := context.WithTimeout(tabCtx, 15*time.Second)
	defer cancelTimeout()

	var links []string
	js := `Array.from(document.querySelectorAll('a[href]')).map(a => a.href).filter(h => h.startsWith('http'))`
	if err := chromedp.Run(timeoutCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(js, &links),
	); err != nil {
		return nil, err
	}

	base, _ := url.Parse(pageURL)
	var clean []string
	seen := make(map[string]bool)
	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil {
			continue
		}
		u.Fragment = ""
		resolved := base.ResolveReference(u).String()
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		if strings.HasPrefix(resolved, "http") &&
			u.Host == base.Host &&
			strings.HasPrefix(u.Path, base.Path) {
			clean = append(clean, resolved)
		}
	}
	return clean, nil
}

var webChromeCandidates = []string{
	"google-chrome",
	"google-chrome-stable",
	"google-chrome-beta",
	"chromium",
	"chromium-browser",
	"brave-browser",
	"brave",
	"brave-origin",
	"chrome",
	"microsoft-edge",
}

func findChrome() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, name := range webChromeCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
