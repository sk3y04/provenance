# Cookies & Authentication

How authentication works across provenance's extractors.

## Two cookie mechanisms

| Mechanism | Flag | Extractor support |
|-----------|------|-------------------|
| **Netscape cookies.txt** | `--cookies <file>` | yt-dlp, Twitter, Reddit, Instagram, Browser (chromedp) |
| **Browser cookie extraction** | `--cookies-from-browser <name>` | yt-dlp only |

They can be mixed: `--cookies-from-browser chrome` for yt-dlp sites + `--cookies cookies.txt` for Twitter/Reddit/Instagram native extractors.

---

## Netscape cookies.txt

The recommended method. Export a `cookies.txt` file from your browser, then pass it with `-c` / `--cookies`.

### Browser extensions

| Browser | Extension |
|---------|-----------|
| Chrome / Edge | [Get cookies.txt LOCALLY](https://chromewebstore.google.com/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc) |
| Firefox | [cookies.txt](https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/) |

### Usage

```bash
provenance grab --cookies cookies.txt https://x.com/<user>
provenance grab --cookies cookies.txt https://www.reddit.com/user/<user>
provenance grab --cookies cookies.txt https://www.instagram.com/<user>/
provenance grab --cookies cookies.txt https://www.patreon.com/posts/...
```

### How each extractor uses the file

#### yt-dlp

Passed directly: `yt-dlp --cookies cookies.txt <url>`.

#### Twitter/X extractor

The file is loaded into memory; cookies are sent as the `Cookie` HTTP header on every GraphQL request. Specifically, the `ct0` cookie is extracted as the CSRF token (`x-csrf-token` header). The `auth_token` cookie is required for authentication.

If `cookiesFile` is empty, the Twitter extractor returns an error: `"cookies file is required for Twitter/X"`.

#### Reddit extractor

The file is loaded and sent as the `Cookie` header on API requests. Reddit works unauthenticated but has lower rate limits (~10 req/min vs ~60 req/min with OAuth). Cookies provide session-based auth without needing OAuth credentials.

#### Instagram extractor

The file is loaded into memory; cookies are sent as the `Cookie` HTTP header on every API request. The `sessionid` cookie is required for authentication. The `csrftoken` cookie is extracted as the CSRF token (`X-CSRFToken` header).

If `cookiesFile` is empty, the Instagram extractor returns an error: `"cookies file is required for Instagram"`.

#### Browser extractor (chromedp)

Cookies are injected into the headless Chrome instance via `network.SetCookie` before navigating to the page. This allows the browser to load authenticated content.

---

## Cookie file format

Netscape format, one cookie per line:

```
# Netscape HTTP Cookie File
.example.com	TRUE	/	FALSE	1740899999	name	value
```

Lines starting with `#HttpOnly_` are parsed as HTTP-only cookies (prefix stripped). Blank lines and `#`-comment lines (without the HttpOnly prefix) are skipped.

### Format fields

```
domain  flag  path  secure  expiration  name  value
```

| Field | Description |
|-------|-------------|
| `domain` | Cookie domain (e.g. `.x.com`) |
| `flag` | `TRUE` or `FALSE` - subdomain match |
| `path` | Cookie path (e.g. `/`) |
| `secure` | `TRUE` or `FALSE` |
| `expiration` | Unix timestamp (seconds) |
| `name` | Cookie name |
| `value` | Cookie value |

The `loadNetscapeCookies()` function in `internal/extractor/browser.go` is shared by all extracts.

---

## Browser cookie extraction (`--cookies-from-browser`)

yt-dlp has built-in support for reading cookies directly from browser profile databases. This eliminates the need to manually export `cookies.txt`.

```bash
provenance grab --cookies-from-browser chrome https://www.patreon.com/posts/...
provenance grab --cookies-from-browser firefox https://www.youtube.com/watch?v=...
```

### Supported browsers

- `chrome` (Google Chrome)
- `chromium` (Chromium)
- `edge` (Microsoft Edge)
- `firefox` (Mozilla Firefox)
- `brave` (Brave Browser)
- `opera` (Opera)
- `vivaldi` (Vivaldi)

### When to use each method

| Method | Use when |
|--------|----------|
| `--cookies` | You need cookies for Twitter/Reddit native extracts, or you want a single file shared across all extractors |
| `--cookies-from-browser` | You only need yt-dlp sites (YouTube, Patreon, etc.) and don't want to export files |
| Both | yt-dlp via `--cookies-from-browser chrome`, Twitter/Reddit via `--cookies cookies.txt` |

---

## Cookie injection paths

```
┌─────────────────────────────────────────────────────┐
│                  `--cookies cookies.txt`             │
│                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐          │
│  │ yt-dlp   │  │  Twitter │  │  Reddit │           │
│  │          │  │Extractor │  │Extractor│           │
│  │--cookies │  │          │  │          │          │
│  │  <file>  │  │ Cookie   │  │ Cookie   │          │
│  │          │  │ header   │  │ header   │          │
│  └──────────┘  └──────────┘  └──────────┘          │
│          │                                             │
│  ┌───────┴──────┐                                     │
│  │   Chromedp   │                                     │
│  │              │                                     │
│  │ network.     │                                     │
│  │ SetCookie()  │                                     │
│  └──────────────┘                                     │
│                                                     │
├─────────────────────────────────────────────────────┤
│            `--cookies-from-browser <name>`           │
│                                                     │
│  ┌──────────┐                                       │
│  │ yt-dlp   │                                       │
│  │          │                                       │
│  │--cookies-│                                       │
│  │ from-    │                                       │
│  │ browser  │                                       │
│  └──────────┘                                       │
└─────────────────────────────────────────────────────┘
```

---

## Twitter/X authentication details

### Required cookies

For the Twitter/X extractor to work, the cookies file must contain:

- `auth_token` - the account's authentication token (required to make authenticated API calls)
- `ct0` - the CSRF token

Without `auth_token`, the extractor cannot make GraphQL requests.

### Guest token fallback

If no cookies are provided, the extractor cannot authenticate (an `auth_token` cookie is required for user timeline access). The guest bearer token is obtained from `TWITTER_BEARER_TOKEN` env var, refreshed at runtime from X.com's web client JS, or falls back to a built-in public guest token.

---

## Reddit OAuth2

### Unauthenticated access

Reddit returns JSON data without authentication, but rate limits are tight (~10 requests/minute).

### OAuth2

Set `REDDIT_CLIENT_ID` and `REDDIT_CLIENT_SECRET` for Basic auth. This increases limits to ~60 requests/minute.

```bash
export REDDIT_CLIENT_ID="your-client-id"
export REDDIT_CLIENT_SECRET="your-client-secret"
export REDDIT_OAUTH_TOKEN="pre-fetched-token"  # optional, takes precedence
```

The OAuth token is sent as the `Authorization` header: `Basic base64(client_id:client_secret)`.

### Cookie-based auth

Session cookies (from `cookies.txt`) also work for browsing Reddit content, providing authenticated rate limits without OAuth setup.
