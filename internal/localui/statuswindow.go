//go:build !nolocalui

package localui

import (
	"fmt"
	"image/color"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

// ── design tokens ─────────────────────────────────────────────────────────────

var (
	colorSurface   = color.NRGBA{R: 15, G: 23, B: 42, A: 255}   // #0f172a slate-900
	colorCard      = color.NRGBA{R: 30, G: 41, B: 59, A: 255}   // #1e293b slate-800
	colorBorder    = color.NRGBA{R: 51, G: 65, B: 85, A: 255}   // #334155 slate-700
	colorText      = color.NRGBA{R: 248, G: 250, B: 252, A: 255} // #f8fafc
	colorMuted     = color.NRGBA{R: 148, G: 163, B: 184, A: 255} // #94a3b8 slate-400
	colorLabel     = color.NRGBA{R: 100, G: 116, B: 139, A: 255} // #64748b slate-500
	colorSuccess   = color.NRGBA{R: 34, G: 197, B: 94, A: 255}  // #22c55e
	colorAmber     = color.NRGBA{R: 245, G: 158, B: 11, A: 255} // #f59e0b
	colorDanger    = color.NRGBA{R: 239, G: 68, B: 68, A: 255}  // #ef4444
	colorTermBg    = color.NRGBA{R: 2, G: 8, B: 23, A: 255}     // #020817

	colorSuccessDim = color.NRGBA{R: 34, G: 197, B: 94, A: 30}
	colorAmberDim   = color.NRGBA{R: 245, G: 158, B: 11, A: 30}
	colorDangerDim  = color.NRGBA{R: 239, G: 68, B: 68, A: 30}
	colorPrimaryDim = color.NRGBA{R: 59, G: 130, B: 246, A: 30}

	approveGreen = colorSuccess
)

// ── state types ───────────────────────────────────────────────────────────────

type runnerState int

const (
	stateDisconnected runnerState = iota
	stateIdle
	stateRunning
)

type jobRecord struct {
	id        string
	image     string
	exitCode  int
	duration  time.Duration
	timestamp time.Time
}

type activeJobCard struct {
	id          string
	image       string
	startedAt   time.Time
	outer       *fyne.Container
	elapsedText *canvas.Text
	stopElapsed func()
}

// ── StatusWindow ──────────────────────────────────────────────────────────────

// StatusWindow is a small always-visible window showing runner status and logs.
// SetConnected/SetIdle/SetJobRunning/AppendLog implement runner.StatusSink and
// are safe to call from any goroutine.
type StatusWindow struct {
	win fyne.Window

	// Header
	hostLabel *canvas.Text
	badgeBg   *canvas.Rectangle
	badgeDot  *canvas.Circle
	badgeText *canvas.Text

	// Stats
	statToday   *canvas.Text
	statSuccess *canvas.Text
	statFailed  *canvas.Text
	statUptime  *canvas.Text

	// Active jobs section (shown only when at least one job is running)
	jobCardOuter *fyne.Container
	activeJobsBox *fyne.Container

	// CTA banner
	ctaBanner *fyne.Container

	// Recent jobs
	recentBox *fyne.Container

	// Log (read-only Entry for text selection support)
	logEntry  *widget.Entry
	logScroll *container.Scroll

	// State tracking
	state       runnerState
	runnerName  string
	apiURL      string
	webURL      string
	history     []jobRecord
	jobsToday   int
	jobsSuccess int
	jobsFailed  int
	startTime   time.Time
	activeMu    sync.Mutex
	activeJobs  map[string]*activeJobCard

	// onOpenSettings is called when the user clicks the settings gear button.
	onOpenSettings func()
}

// NewStatusWindow creates the status window (does not show it yet).
func NewStatusWindow(runnerName, apiURL string) *StatusWindow {
	a := getApp()
	win := a.NewWindow("WorkflowFiesta Runner")
	win.SetCloseIntercept(func() {
		icon := canvas.NewText("●", colorSuccess)
		icon.TextSize = 16

		title := widget.NewLabel("Still running in the background")
		title.TextStyle = fyne.TextStyle{Bold: true}

		body := widget.NewLabel("WorkflowFiesta Runner will continue running after you close this window. To reopen it or quit, right-click the WorkflowFiesta icon in your system tray (bottom-right corner of the taskbar).")
		body.Wrapping = fyne.TextWrapWord

		content := container.NewVBox(
			container.NewHBox(icon, title),
			container.NewPadded(body),
		)

		d := dialog.NewCustomConfirm(
			"",
			"Keep Running",
			"Quit Runner",
			content,
			func(keepRunning bool) {
				if keepRunning {
					win.Hide()
				} else {
					QuitApp()
				}
			},
			win,
		)
		d.Show()
	})

	sw := &StatusWindow{
		win:        win,
		runnerName: runnerName,
		apiURL:     apiURL,
		webURL:     deriveWebURL(apiURL),
		startTime:  time.Now(),
		activeJobs: make(map[string]*activeJobCard),
	}

	content := container.NewVBox(
		sw.buildHeader(),
		sw.buildStatsStrip(),
		sw.buildCTABanner(),
		sw.buildJobCard(),
		sw.buildRecentJobs(),
		sw.buildTerminal(),
	)

	win.SetContent(content)
	savedW := float32(a.Preferences().FloatWithFallback("status.window.width", 480))
	savedH := float32(a.Preferences().FloatWithFallback("status.window.height", 580))
	win.Resize(fyne.NewSize(savedW, savedH))
	win.SetFixedSize(false)

	// Persist window size after the user manually resizes it.
	// Poll every second; write prefs only after 3 consecutive stable readings
	// to avoid spurious saves during content-triggered reflows.
	go func() {
		var prevW, prevH float32
		var stable int
		sizeTicker := time.NewTicker(time.Second)
		defer sizeTicker.Stop()
		for range sizeTicker.C {
			fyne.Do(func() {
				sz := win.Canvas().Size()
				if sz.Width == prevW && sz.Height == prevH {
					stable++
					if stable == 3 {
						a.Preferences().SetFloat("status.window.width", float64(sz.Width))
						a.Preferences().SetFloat("status.window.height", float64(sz.Height))
					}
				} else {
					prevW, prevH = sz.Width, sz.Height
					stable = 0
				}
			})
		}
	}()

	// Refresh uptime display every minute
	go sw.uptimeTicker()

	return sw
}

// ── section builders ──────────────────────────────────────────────────────────

func (sw *StatusWindow) buildHeader() fyne.CanvasObject {
	// Icon cell — WorkflowFiesta logo
	logoImg := canvas.NewImageFromResource(LogoResource())
	logoImg.FillMode = canvas.ImageFillContain
	iconCell := container.New(layout.NewGridWrapLayout(fyne.NewSize(38, 38)), logoImg)

	// Name + host
	nameLabel := canvas.NewText(sw.runnerName, colorText)
	nameLabel.TextSize = 13
	nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	sw.hostLabel = canvas.NewText("connecting...", colorMuted)
	sw.hostLabel.TextSize = 11
	infoCol := container.NewVBox(
		container.NewWithoutLayout(nameLabel),
		container.NewWithoutLayout(sw.hostLabel),
	)

	// Badge
	sw.badgeDot = canvas.NewCircle(colorDanger)
	sw.badgeDot.StrokeWidth = 0
	dotCell := container.New(layout.NewGridWrapLayout(fyne.NewSize(8, 8)), sw.badgeDot)
	sw.badgeText = canvas.NewText("Offline", colorDanger)
	sw.badgeText.TextSize = 11
	sw.badgeText.TextStyle = fyne.TextStyle{Bold: true}
	badgeInner := container.NewHBox(
		container.NewPadded(dotCell),
		sw.badgeText,
		layout.NewSpacer(),
	)
	sw.badgeBg = canvas.NewRectangle(colorDangerDim)
	sw.badgeBg.CornerRadius = 10
	badge := container.NewStack(sw.badgeBg, container.NewPadded(badgeInner))

	// Gear / settings button
	settingsBtn := newButton("⚙", func() {
		if sw.onOpenSettings != nil {
			sw.onOpenSettings()
		}
	})
	settingsBtn.Importance = widget.LowImportance

	row := container.NewHBox(
		iconCell,
		container.NewPadded(infoCol),
		layout.NewSpacer(),
		badge,
		settingsBtn,
	)

	bg := canvas.NewRectangle(colorSurface)
	bg.StrokeColor = colorBorder
	bg.StrokeWidth = 1

	return container.NewStack(bg, container.NewPadded(row))
}

func (sw *StatusWindow) buildStatsStrip() fyne.CanvasObject {
	sw.statToday = makeStatValue("0", colorText)
	sw.statSuccess = makeStatValue("0", colorSuccess)
	sw.statFailed = makeStatValue("0", colorDanger)
	sw.statUptime = makeStatValue("0s", colorMuted)

	cells := container.NewGridWithColumns(4,
		makeStatCell(sw.statToday, "TODAY"),
		makeStatCell(sw.statSuccess, "PASSED"),
		makeStatCell(sw.statFailed, "FAILED"),
		makeStatCell(sw.statUptime, "UPTIME"),
	)
	bg := canvas.NewRectangle(colorCard)
	bg.StrokeColor = colorBorder
	bg.StrokeWidth = 1
	return container.NewStack(bg, cells)
}

func (sw *StatusWindow) buildJobCard() fyne.CanvasObject {
	sectionLabel := canvas.NewText("ACTIVE JOBS", colorLabel)
	sectionLabel.TextSize = 10
	sectionLabel.TextStyle = fyne.TextStyle{Bold: true}

	sw.activeJobsBox = container.NewVBox()

	bg := canvas.NewRectangle(colorSurface)
	bg.StrokeColor = colorBorder
	bg.StrokeWidth = 1

	inner := container.NewVBox(
		container.NewPadded(container.NewWithoutLayout(sectionLabel)),
		sw.activeJobsBox,
	)
	sw.jobCardOuter = container.NewStack(bg, inner)
	sw.jobCardOuter.Hide()
	return sw.jobCardOuter
}

func (sw *StatusWindow) buildRecentJobs() fyne.CanvasObject {
	sectionLabel := canvas.NewText("RECENT JOBS", colorLabel)
	sectionLabel.TextSize = 10
	sectionLabel.TextStyle = fyne.TextStyle{Bold: true}

	sw.recentBox = container.NewVBox()

	bg := canvas.NewRectangle(colorSurface)
	bg.StrokeColor = colorBorder
	bg.StrokeWidth = 1

	inner := container.NewVBox(
		container.NewPadded(container.NewWithoutLayout(sectionLabel)),
		sw.recentBox,
	)
	return container.NewStack(bg, inner)
}

func (sw *StatusWindow) buildTerminal() fyne.CanvasObject {
	termLabel := canvas.NewText("OUTPUT", colorLabel)
	termLabel.TextSize = 10
	termLabel.TextStyle = fyne.TextStyle{Bold: true}

	clearBtn := newButton("Clear", func() {
		fyne.Do(func() {
			sw.logEntry.SetText("")
		})
	})
	clearBtn.Importance = widget.LowImportance

	termHeaderBg := canvas.NewRectangle(colorCard)
	termHeaderBg.StrokeColor = colorBorder
	termHeaderBg.StrokeWidth = 1
	termHeaderRow := container.NewHBox(
		container.NewPadded(container.NewWithoutLayout(termLabel)),
		layout.NewSpacer(),
		clearBtn,
	)
	termHeader := container.NewStack(termHeaderBg, container.NewPadded(termHeaderRow))

	// Use a multi-line Entry so users can select and copy output text.
	// Wrapping is off so long lines stay on one line for easier selection.
	sw.logEntry = widget.NewMultiLineEntry()
	sw.logEntry.Wrapping = fyne.TextWrapOff
	sw.logEntry.TextStyle = fyne.TextStyle{Monospace: true}

	sw.logScroll = container.NewScroll(sw.logEntry)
	sw.logScroll.SetMinSize(fyne.NewSize(0, 160))

	termBodyBg := canvas.NewRectangle(colorTermBg)
	termBodyBg.StrokeColor = colorBorder
	termBodyBg.StrokeWidth = 1
	termBody := container.NewStack(termBodyBg, container.NewPadded(sw.logScroll))

	return container.NewPadded(container.NewVBox(termHeader, termBody))
}

func (sw *StatusWindow) buildCTABanner() fyne.CanvasObject {
	colorCTABg     := color.NRGBA{R: 0xF3, G: 0x61, B: 0x0C, A: 255} // brand orange
	colorCTABgDim  := color.NRGBA{R: 0xF3, G: 0x61, B: 0x0C, A: 25}
	colorCTABorder := color.NRGBA{R: 0xF3, G: 0x61, B: 0x0C, A: 80}

	// Icon — lightning bolt indicator
	iconBar := canvas.NewRectangle(colorCTABg)
	iconBar.CornerRadius = 2
	iconBg := canvas.NewRectangle(colorCTABgDim)
	iconBg.CornerRadius = 4
	iconBg.StrokeColor = colorCTABorder
	iconBg.StrokeWidth = 1
	icon := container.NewStack(
		container.New(layout.NewGridWrapLayout(fyne.NewSize(32, 32)), iconBg),
		container.NewCenter(container.New(layout.NewGridWrapLayout(fyne.NewSize(10, 20)), iconBar)),
	)

	// Text
	headline := canvas.NewText("Your runner is live!", color.NRGBA{R: 0xFF, G: 0xE0, B: 0xC8, A: 255})
	headline.TextSize = 13
	headline.TextStyle = fyne.TextStyle{Bold: true}

	subline := canvas.NewText("Your agent can now read and interact with data on this computer.", color.NRGBA{R: 0xF9, G: 0xBC, B: 0x8F, A: 255})
	subline.TextSize = 11

	textCol := container.NewVBox(
		container.NewWithoutLayout(headline),
		container.NewWithoutLayout(subline),
	)

	// Buttons
	openBtn := newButton("Start Working →", func() {
		u, err := url.Parse(sw.webURL)
		if err == nil {
			fyne.CurrentApp().OpenURL(u)
		}
	})
	openBtn.Importance = widget.HighImportance

	btnRow := container.NewHBox(openBtn)

	inner := container.NewVBox(
		container.NewHBox(icon, container.NewPadded(textCol)),
		container.NewPadded(btnRow),
	)

	bg := canvas.NewRectangle(colorCTABgDim)
	bg.CornerRadius = 6
	bg.StrokeColor = colorCTABorder
	bg.StrokeWidth = 1

	sw.ctaBanner = container.NewVBox(container.NewPadded(container.NewStack(bg, container.NewPadded(inner))))
	sw.ctaBanner.Hide()
	return sw.ctaBanner
}

// deriveWebURL converts an API URL to the likely web app URL.
// e.g. "http://host/api" → "http://host", "http://host:3001" → "http://host:3000"
func deriveWebURL(apiURL string) string {
	// Strip trailing /api path segment
	trimmed := strings.TrimSuffix(strings.TrimRight(apiURL, "/"), "/api")
	if trimmed != apiURL && trimmed != "" {
		return trimmed
	}
	// Common local dev: port 3001 → 3000
	if strings.Contains(apiURL, ":3001") {
		return strings.Replace(apiURL, ":3001", ":3000", 1)
	}
	return apiURL
}

// ── StatusSink implementation ─────────────────────────────────────────────────

// Show makes the window visible.
func (sw *StatusWindow) Show() {
	sw.win.Show()
}

// SetOnOpenSettings registers a callback to be invoked when the user clicks
// the settings gear button in the status window header.
func (sw *StatusWindow) SetOnOpenSettings(fn func()) {
	sw.onOpenSettings = fn
}

// SetConnected updates the connection indicator.
func (sw *StatusWindow) SetConnected(connected bool) {
	fyne.Do(func() {
		if connected {
			if sw.activeJobCount() > 0 {
				sw.state = stateRunning
			} else {
				sw.state = stateIdle
			}
			sw.hostLabel.Text = sw.apiURL
			sw.hostLabel.Color = colorMuted
			sw.ctaBanner.Show()
		} else {
			sw.state = stateDisconnected
			sw.hostLabel.Text = "connecting..."
			sw.hostLabel.Color = colorLabel
			sw.ctaBanner.Hide()
		}
		sw.hostLabel.Refresh()
		sw.ctaBanner.Refresh()
		sw.refreshBadge()
	})
}

// SetIdle clears the current job info and returns the badge to green.
func (sw *StatusWindow) SetIdle() {
	fyne.Do(func() {
		if sw.activeJobCount() == 0 {
			sw.state = stateIdle
			sw.jobCardOuter.Hide()
		} else {
			sw.state = stateRunning
			sw.jobCardOuter.Show()
		}
		sw.refreshBadge()
	})
}

// SetJobRunning updates the display with the running job ID, image, and script blurb.
func (sw *StatusWindow) SetJobRunning(jobID, image, scriptBlurb string) {
	sw.activeMu.Lock()
	if _, exists := sw.activeJobs[jobID]; !exists {
		sw.jobsToday++
		sw.activeJobs[jobID] = makeActiveJobCard(jobID, image, scriptBlurb)
	}
	sw.activeMu.Unlock()
	fyne.Do(func() {
		sw.refreshActiveJobsUI()
		sw.state = stateRunning
		sw.statToday.Text = fmt.Sprintf("%d", sw.jobsToday)
		sw.statToday.Refresh()
		sw.jobCardOuter.Show()
		sw.refreshBadge()
	})

	line := strings.Repeat("─", 50)
	sw.appendToLog(fmt.Sprintf("┌%s┐\n│  ► JOB %-42s│\n├%s┤\n│  ID    : %-40s│\n│  Image : %-40s│\n└%s┘\n",
		line, jobID,
		line,
		truncate(jobID, 40),
		truncate(image, 40),
		line,
	))
}

// SetJobComplete records a completed job in history (call before SetIdle).
func (sw *StatusWindow) SetJobComplete(jobID, image string, exitCode int) {
	sw.activeMu.Lock()
	card := sw.activeJobs[jobID]
	if card != nil {
		card.stopElapsed()
		delete(sw.activeJobs, jobID)
	}
	sw.activeMu.Unlock()

	dur := time.Duration(0)
	if card != nil {
		dur = time.Since(card.startedAt)
	}
	rec := jobRecord{
		id:        jobID,
		image:     image,
		exitCode:  exitCode,
		duration:  dur,
		timestamp: time.Now(),
	}
	if exitCode == 0 {
		sw.jobsSuccess++
	} else {
		sw.jobsFailed++
	}
	sw.history = append([]jobRecord{rec}, sw.history...)
	if len(sw.history) > 5 {
		sw.history = sw.history[:5]
	}

	fyne.Do(func() {
		sw.refreshActiveJobsUI()
		if sw.activeJobCount() == 0 {
			sw.state = stateIdle
			sw.jobCardOuter.Hide()
		} else {
			sw.state = stateRunning
			sw.jobCardOuter.Show()
		}
		sw.statSuccess.Text = fmt.Sprintf("%d", sw.jobsSuccess)
		sw.statSuccess.Refresh()
		sw.statFailed.Text = fmt.Sprintf("%d", sw.jobsFailed)
		sw.statFailed.Refresh()
		sw.rebuildRecentJobs()
		sw.refreshBadge()
	})
}

// AppendLog adds a chunk of output to the log view and mirrors it to stdout.
func (sw *StatusWindow) AppendLog(chunk string) {
	fmt.Fprint(os.Stdout, chunk)
	sw.appendToLog(chunk)
}

// ── internal helpers ──────────────────────────────────────────────────────────

func (sw *StatusWindow) refreshBadge() {
	switch sw.state {
	case stateDisconnected:
		sw.badgeBg.FillColor = colorDangerDim
		sw.badgeDot.FillColor = colorDanger
		sw.badgeText.Text = "Offline"
		sw.badgeText.Color = colorDanger
	case stateIdle:
		sw.badgeBg.FillColor = colorSuccessDim
		sw.badgeDot.FillColor = colorSuccess
		sw.badgeText.Text = "Idle"
		sw.badgeText.Color = colorSuccess
	case stateRunning:
		sw.badgeBg.FillColor = colorAmberDim
		sw.badgeDot.FillColor = colorAmber
		sw.badgeText.Text = "Running"
		sw.badgeText.Color = colorAmber
	}
	sw.badgeBg.Refresh()
	canvas.Refresh(sw.badgeDot)
	sw.badgeText.Refresh()
}

func (sw *StatusWindow) rebuildRecentJobs() {
	var rows []fyne.CanvasObject
	for _, r := range sw.history {
		rows = append(rows, makeJobRow(r))
	}
	sw.recentBox.Objects = rows
	sw.recentBox.Refresh()
}

const maxLogBytes = 50 * 1024 // 50 KB cap — trim oldest content when exceeded

func (sw *StatusWindow) appendToLog(chunk string) {
	fyne.Do(func() {
		current := sw.logEntry.Text
		combined := current + chunk
		// Trim oldest content if we exceed the cap
		if len(combined) > maxLogBytes {
			// Drop the first half to avoid constant trimming
			combined = combined[len(combined)-maxLogBytes/2:]
			// Align to next newline boundary
			if idx := strings.Index(combined, "\n"); idx >= 0 {
				combined = combined[idx+1:]
			}
		}
		sw.logEntry.SetText(combined)
		sw.logScroll.ScrollToBottom()
	})
}

func makeActiveJobCard(jobID, image, scriptBlurb string) *activeJobCard {
	cardTitle := canvas.NewText("RUNNING", colorAmber)
	cardTitle.TextSize = 10
	cardTitle.TextStyle = fyne.TextStyle{Bold: true}

	jobIDLabel := canvas.NewText(jobID, colorMuted)
	jobIDLabel.TextSize = 11

	elapsed := canvas.NewText("00:00", colorAmber)
	elapsed.TextSize = 11
	elapsed.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	titleRow := container.NewHBox(
		container.NewWithoutLayout(cardTitle),
		container.NewPadded(container.NewWithoutLayout(jobIDLabel)),
		layout.NewSpacer(),
		container.NewWithoutLayout(elapsed),
	)

	imageLabel := canvas.NewText("🐳 "+image, colorMuted)
	imageLabel.TextSize = 11
	imgPillBg := canvas.NewRectangle(colorPrimaryDim)
	imgPillBg.CornerRadius = 8
	imgPillBg.StrokeColor = color.NRGBA{R: 59, G: 130, B: 246, A: 60}
	imgPillBg.StrokeWidth = 1
	imgPill := container.NewStack(imgPillBg, container.NewPadded(container.NewWithoutLayout(imageLabel)))

	scriptLabel := widget.NewLabel(scriptBlurb)
	scriptLabel.TextStyle = fyne.TextStyle{Monospace: true}
	scriptLabel.Wrapping = fyne.TextTruncate
	scriptBg := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 50})
	scriptBg.CornerRadius = 3
	scriptBlock := container.NewStack(scriptBg, container.NewPadded(scriptLabel))

	cardBg := canvas.NewRectangle(colorAmberDim)
	cardBg.CornerRadius = 6
	cardBg.StrokeColor = color.NRGBA{R: 245, G: 158, B: 11, A: 60}
	cardBg.StrokeWidth = 1

	inner := container.NewVBox(titleRow, imgPill, scriptBlock)
	outer := container.NewVBox(container.NewPadded(container.NewStack(cardBg, container.NewPadded(inner))))

	startedAt := time.Now()
	stopped := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-t.C:
				txt := fmt.Sprintf("%02d:%02d", int(time.Since(startedAt).Minutes()), int(time.Since(startedAt).Seconds())%60)
				fyne.Do(func() {
					elapsed.Text = txt
					elapsed.Refresh()
				})
			}
		}
	}()

	stopFn := func() {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}

	return &activeJobCard{
		id:          jobID,
		image:       image,
		startedAt:   startedAt,
		outer:       outer,
		elapsedText: elapsed,
		stopElapsed: stopFn,
	}
}

func (sw *StatusWindow) refreshActiveJobsUI() {
	var cards []*activeJobCard
	sw.activeMu.Lock()
	for _, c := range sw.activeJobs {
		cards = append(cards, c)
	}
	sw.activeMu.Unlock()
	// Stable order by start time so cards don't jump.
	for i := 0; i < len(cards)-1; i++ {
		for j := i + 1; j < len(cards); j++ {
			if cards[i].startedAt.After(cards[j].startedAt) {
				cards[i], cards[j] = cards[j], cards[i]
			}
		}
	}
	rows := make([]fyne.CanvasObject, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, c.outer)
	}
	sw.activeJobsBox.Objects = rows
	sw.activeJobsBox.Refresh()
}

func (sw *StatusWindow) activeJobCount() int {
	sw.activeMu.Lock()
	defer sw.activeMu.Unlock()
	return len(sw.activeJobs)
}

func (sw *StatusWindow) uptimeTicker() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		elapsed := time.Since(sw.startTime)
		h := int(elapsed.Hours())
		m := int(elapsed.Minutes()) % 60
		s := int(elapsed.Seconds()) % 60
		var txt string
		if h > 0 {
			txt = fmt.Sprintf("%02d:%02d", h, m)
		} else {
			txt = fmt.Sprintf("%02d:%02d", m, s)
		}
		fyne.Do(func() {
			sw.statUptime.Text = txt
			sw.statUptime.Refresh()
		})
	}
}

// ── widget factory helpers ────────────────────────────────────────────────────

func makeStatValue(text string, col color.Color) *canvas.Text {
	t := canvas.NewText(text, col)
	t.TextSize = 18
	t.TextStyle = fyne.TextStyle{Bold: true}
	return t
}

func makeStatCell(value *canvas.Text, label string) fyne.CanvasObject {
	lbl := canvas.NewText(label, colorLabel)
	lbl.TextSize = 9
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	// Use HBox spacers instead of container.NewCenter: NewCenter reports {0,0}
	// MinSize which causes canvas.Text children to go blank on VBox reflow.
	return container.NewVBox(
		container.NewHBox(layout.NewSpacer(), value, layout.NewSpacer()),
		container.NewHBox(layout.NewSpacer(), lbl, layout.NewSpacer()),
	)
}

func makeJobRow(r jobRecord) fyne.CanvasObject {
	// Status dot
	dotColor := colorSuccess
	dotBgColor := colorSuccessDim
	mark := "✓"
	if r.exitCode != 0 {
		dotColor = colorDanger
		dotBgColor = colorDangerDim
		mark = "✗"
	}
	dotBg := canvas.NewRectangle(dotBgColor)
	dotBg.CornerRadius = 9
	dotText := canvas.NewText(mark, dotColor)
	dotText.TextSize = 9
	dotText.TextStyle = fyne.TextStyle{Bold: true}
	dotCell := container.NewStack(
		container.New(layout.NewGridWrapLayout(fyne.NewSize(18, 18)), dotBg),
		container.NewCenter(dotText),
	)

	idLabel := canvas.NewText(r.id, colorText)
	idLabel.TextSize = 11
	idLabel.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

	imgLabel := canvas.NewText(r.image, colorMuted)
	imgLabel.TextSize = 11

	age := timeAgo(r.timestamp)
	ageLabel := canvas.NewText(age, colorLabel)
	ageLabel.TextSize = 11

	return container.NewPadded(container.NewHBox(
		dotCell,
		container.NewVBox(
			container.NewWithoutLayout(idLabel),
			container.NewWithoutLayout(imgLabel),
		),
		layout.NewSpacer(),
		container.NewWithoutLayout(ageLabel),
	))
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
