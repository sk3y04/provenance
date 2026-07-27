// Package tui implements an interactive terminal UI for provenance.
package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sk3y04/provenance/internal/archive"
	"github.com/sk3y04/provenance/internal/collection"
	"github.com/sk3y04/provenance/internal/config"
	"github.com/sk3y04/provenance/internal/history"
	"github.com/sk3y04/provenance/internal/manifest"
	"github.com/sk3y04/provenance/internal/ratelimit"
	"github.com/sk3y04/provenance/internal/session"
	"github.com/sk3y04/provenance/internal/watch"
)

// Run launches the TUI and blocks until the user quits.
func Run(ctx context.Context) error {
	rl := ratelimit.New()
	m := newModel(ctx, rl)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.program = p
	_, err := p.Run()
	return err
}

// ---------------------------------------------------------------------------
// View / state
// ---------------------------------------------------------------------------

type view int

const (
	viewMain view = iota
	viewSessions
	viewSessionDetail
	viewWatches
	viewHistory
	viewNewDownload
	viewScanPick
	viewRunner
	viewArchiveSearch
	viewCollections
	viewCollectionDetail
	viewVault
)

const confirmWindow = 3 * time.Second

type model struct {
	ctx         context.Context
	program     *tea.Program
	rateLimiter *ratelimit.Manager

	view view
	err  string
	info string

	width, height int

	// main menu
	mainCursor       int
	resumeCandidate  string // most-recent session with pending/failed work
	resumePendingURL int    // count for the banner

	// sessions
	sessions     []session.Info
	sessCursor   int
	sessSelected *session.Session
	sessFilter   filterState
	sessPending  pendingDelete

	// watches
	watches      []watch.Subscription
	watchesCur   int
	watchesFilt  filterState
	watchPending pendingDelete
	watchAddForm *watchAddForm

	// history
	history     []history.Run
	historyCur  int
	historyFilt filterState

	// new download form
	form         downloadForm
	formStep     int
	showAdvanced bool
	preview      scanPreview
	cookiePick   cookiePickerState

	// scan & pick
	scan scanState

	// runner
	runner runnerState

	// archive search
	archiveQuery   textinput.Model
	archiveResults []archiveSearchHit
	archiveCur     int
	archiveLoading bool

	// collections
	collections  []collection.Collection
	collCur      int
	collSelected *collection.Collection
	collFilter   filterState

	// vault
	vaultCols []archive.ArchiveCollection
	vaultCur  int
}

type archiveSearchHit struct {
	Title      string
	Headline   string
	URL        string
	Collection string
	Revision   string
	Date       string
}

type filterState struct {
	active bool
	input  textinput.Model
}

type pendingDelete struct {
	name string
	at   time.Time
}

type downloadForm struct {
	url         textinput.Model
	output      textinput.Model
	concurrency textinput.Model
	cookies     textinput.Model
	sessionName textinput.Model
	// advanced
	quality        textinput.Model
	cookiesBrowser textinput.Model
	includeExt     textinput.Model
	excludeExt     textinput.Model
	minSize        textinput.Model
	maxSize        textinput.Model
	titleMatch     textinput.Model
	titleExclude   textinput.Model
	postLimit      textinput.Model
	outputLayout   textinput.Model
	outputTemplate textinput.Model
	speedLimit     textinput.Model
	audioOnly      bool
	noArchive      bool
	includePosts   bool
}

// scanPreview is shown under the URL field of the new-download form once the
// user pauses typing for a moment. It runs dispatcher.Scan in the background.
type scanPreview struct {
	url     string // URL the preview was started for
	loading bool
	count   int
	size    int64
	site    string
	err     string
}

type scanPreviewMsg struct {
	url      string
	count    int
	size     int64
	site     string
	err      error
	canceled bool
}

type scanPreviewTick struct {
	url string
}

// scanState backs the "scan & pick" view: a multi-selectable list of
// manifest items the user can subset before downloading.
type scanState struct {
	sourceURL    string
	loading      bool
	err          string
	items        []manifest.Item
	checked      map[int]bool
	cursor       int
	filter       filterState
	urlInput     textinput.Model // shown when no URL is set yet
	awaitURL     bool
	site         string
	showAdvanced bool
	advForm      advOptsForm
}

type advOptsForm struct {
	quality        textinput.Model
	cookiesBrowser textinput.Model
	includeExt     textinput.Model
	excludeExt     textinput.Model
	minSize        textinput.Model
	maxSize        textinput.Model
	titleMatch     textinput.Model
	titleExclude   textinput.Model
	postLimit      textinput.Model
	outputLayout   textinput.Model
	outputTemplate textinput.Model
	speedLimit     textinput.Model
	audioOnly      bool
	noArchive      bool
	includePosts   bool
	step           int
}

func newAdvOptsForm() advOptsForm {
	mk := func(placeholder, value string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.SetValue(value)
		ti.Width = 40
		return ti
	}
	return advOptsForm{
		quality:        mk("best", "best"),
		cookiesBrowser: mk("e.g. chrome, firefox, brave", ""),
		includeExt:     mk("e.g. mp4,jpg,zip", ""),
		excludeExt:     mk("e.g. psd,zip", ""),
		minSize:        mk("e.g. 10MB", ""),
		maxSize:        mk("e.g. 2GB", ""),
		titleMatch:     mk("regex to include", ""),
		titleExclude:   mk("regex to exclude", ""),
		postLimit:      mk("e.g. 10", ""),
		outputLayout:   mk("creator, site, flat, date", ""),
		outputTemplate: mk("advanced yt-dlp template", ""),
		speedLimit:     mk("e.g. 5MB, 500KB", ""),
	}
}

type scanLoadedMsg struct {
	sourceURL string
	manifest  manifest.Manifest
	err       error
}

type runnerState struct {
	title    string
	urls     []string
	options  config.Config
	logs     []string
	queued   int
	running  int
	ok       int
	failed   int
	skipped  int
	done     bool
	err      error
	cancel   context.CancelFunc
	startAt  time.Time
	maxLines int
	notified bool

	// Per-file live progress: keyed by URL. order preserves first-seen.
	files     map[string]*fileProgress
	fileOrder []string
	bar       progress.Model // shared template; we render manually per row
	// throughput sampling for speed/ETA.
	lastSampleAt    time.Time
	lastSampleBytes int64
	speedBps        float64
}

type fileProgress struct {
	url       string
	dest      string
	total     int64
	written   int64
	startedAt time.Time
	doneAt    time.Time
	done      bool
	err       error
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type sessionsLoadedMsg struct {
	infos []session.Info
	err   error
}

type sessionLoadedMsg struct {
	s   *session.Session
	err error
}

type watchesLoadedMsg struct {
	subs []watch.Subscription
	err  error
}

type historyLoadedMsg struct {
	runs []history.Run
	err  error
}

type runnerEventMsg struct {
	kind string // queue|start|ok|fail|skip
	url  string
	note string
}

type runnerLogMsg struct {
	line string
}

type runnerDoneMsg struct {
	err error
}

type fileStartMsg struct {
	url, dest string
	total     int64
}

type fileProgressMsg struct {
	url            string
	written, total int64
}

type fileDoneMsg struct {
	url string
	err error
}

type cookiePickerState struct {
	active bool
	files  []string
	cursor int
}

type cookiesFoundMsg struct {
	files []string
	err   error
}

type tickMsg time.Time

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func newModel(ctx context.Context, rl *ratelimit.Manager) *model {
	m := &model{ctx: ctx, view: viewMain, rateLimiter: rl}
	m.form = newDownloadForm()
	m.sessFilter.input = newFilterInput()
	m.watchesFilt.input = newFilterInput()
	m.historyFilt.input = newFilterInput()
	m.scan.filter.input = newFilterInput()
	m.scan.urlInput = textinput.New()
	m.scan.urlInput.Placeholder = "x.com/..., reddit.com/..., etc."
	m.scan.urlInput.Width = 70
	m.scan.checked = map[int]bool{}
	m.scan.advForm = newAdvOptsForm()
	m.collFilter.input = newFilterInput()
	m.archiveQuery = textinput.New()
	m.archiveQuery.Placeholder = "search archived content..."
	m.archiveQuery.Width = 50
	return m
}

func newFilterInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "filter..."
	ti.Prompt = "/ "
	ti.Width = 40
	return ti
}

const basicStepCount = 5

func (m *model) formStepCount() int {
	if m.showAdvanced {
		return basicStepCount + len(advFieldSpecDefs)
	}
	return basicStepCount
}

type advFieldSpec struct {
	label  string
	input  *textinput.Model
	isBool bool
	boolP  *bool
}

var advFieldSpecDefs = []struct {
	label  string
	isBool bool
}{
	{"Quality", false},
	{"Cookies from browser", false},
	{"Include extensions", false},
	{"Exclude extensions", false},
	{"Min size", false},
	{"Max size", false},
	{"Title match", false},
	{"Title exclude", false},
	{"Post limit", false},
	{"Output layout", false},
	{"Filename template", false},
	{"Speed limit", false},
	{"Audio only", true},
	{"No archive", true},
	{"Include posts", true},
}

func (m *model) advFieldSpecs() []advFieldSpec {
	return []advFieldSpec{
		{advFieldSpecDefs[0].label, &m.form.quality, false, nil},
		{advFieldSpecDefs[1].label, &m.form.cookiesBrowser, false, nil},
		{advFieldSpecDefs[2].label, &m.form.includeExt, false, nil},
		{advFieldSpecDefs[3].label, &m.form.excludeExt, false, nil},
		{advFieldSpecDefs[4].label, &m.form.minSize, false, nil},
		{advFieldSpecDefs[5].label, &m.form.maxSize, false, nil},
		{advFieldSpecDefs[6].label, &m.form.titleMatch, false, nil},
		{advFieldSpecDefs[7].label, &m.form.titleExclude, false, nil},
		{advFieldSpecDefs[8].label, &m.form.postLimit, false, nil},
		{advFieldSpecDefs[9].label, &m.form.outputLayout, false, nil},
		{advFieldSpecDefs[10].label, &m.form.outputTemplate, false, nil},
		{advFieldSpecDefs[11].label, &m.form.speedLimit, false, nil},
		{advFieldSpecDefs[12].label, nil, true, &m.form.audioOnly},
		{advFieldSpecDefs[13].label, nil, true, &m.form.noArchive},
		{advFieldSpecDefs[14].label, nil, true, &m.form.includePosts},
	}
}

func (f *advOptsForm) inputs() []*textinput.Model {
	return []*textinput.Model{
		&f.quality, &f.cookiesBrowser, &f.includeExt, &f.excludeExt,
		&f.minSize, &f.maxSize, &f.titleMatch, &f.titleExclude,
		&f.postLimit, &f.outputLayout, &f.outputTemplate, &f.speedLimit,
	}
}

func (f *advOptsForm) spec(idx int) (string, bool) {
	if idx >= 0 && idx < len(advFieldSpecDefs) {
		return advFieldSpecDefs[idx].label, advFieldSpecDefs[idx].isBool
	}
	return "", false
}

func (f *advOptsForm) boolPtr(idx int) *bool {
	switch idx {
	case 12:
		return &f.audioOnly
	case 13:
		return &f.noArchive
	case 14:
		return &f.includePosts
	}
	return nil
}

func (f *advOptsForm) inputPtr(idx int) *textinput.Model {
	ins := f.inputs()
	if idx >= 0 && idx < len(ins) {
		return ins[idx]
	}
	return nil
}

func newDownloadForm() downloadForm {
	mk := func(placeholder, value string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.SetValue(value)
		ti.Width = 60
		return ti
	}
	mk40 := func(placeholder, value string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.SetValue(value)
		ti.Width = 40
		return ti
	}
	df := downloadForm{
		url:            mk("https://...", ""),
		output:         mk("./downloads", "./downloads"),
		concurrency:    mk("4", "4"),
		cookies:        mk("(optional) ctrl+f to find cookies.txt", ""),
		sessionName:    mk("(optional) session name", ""),
		quality:        mk40("best", "best"),
		cookiesBrowser: mk40("e.g. chrome, firefox, brave", ""),
		includeExt:     mk40("e.g. mp4,jpg,zip", ""),
		excludeExt:     mk40("e.g. psd,zip", ""),
		minSize:        mk40("e.g. 10MB", ""),
		maxSize:        mk40("e.g. 2GB", ""),
		titleMatch:     mk40("regex to include", ""),
		titleExclude:   mk40("regex to exclude", ""),
		postLimit:      mk40("e.g. 10", ""),
		outputLayout:   mk40("creator, site, flat, date", ""),
		outputTemplate: mk40("advanced yt-dlp template", ""),
		speedLimit:     mk40("e.g. 5MB, 500KB", ""),
	}
	df.url.Focus()
	return df
}

func (m *model) Init() tea.Cmd {
	// On startup, load sessions so we can show a resume banner.
	return m.loadSessionsCmd()
}
