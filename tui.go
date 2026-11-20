package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tui "github.com/grindlemire/go-tui"
)

var tuiActive bool

var (
	titleStyle = tui.NewStyle().Bold().Foreground(tui.Cyan)
	dimStyle   = tui.NewStyle().Foreground(tui.BrightBlack)
	okStyle    = tui.NewStyle().Foreground(tui.Green)
	warnStyle  = tui.NewStyle().Foreground(tui.Yellow)
	errStyle   = tui.NewStyle().Foreground(tui.Red)
	boldStyle  = tui.NewStyle().Bold()
	promptCh   = "❯ "
	spinner    = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
)

const (
	maxLogLines   = 1000
	pollInterval  = 5 * time.Second
	maxDeviceRows = 8
	maxCodeWidth  = 28
	sideMinWidth  = 72
)

type pane int

const (
	paneDevices pane = iota
	paneControls
	paneCommand
)

var loginLabels = [3]string{"Email", "Password", "Country code"}

type loginForm struct {
	fields [3]lineEdit
	active int
}

// 選択肢, 値入力, 送信確認のいずれか
type dialog struct {
	prop    propView
	options []string
	idx     int
	editing bool
	edit    lineEdit
	err     string
	confirm bool
	value   interface{}
}

type consoleUI struct {
	app  *tui.App
	c    *Client
	in   inbox
	jobs chan job

	focus     pane
	sideShown bool

	lines  []logLine
	follow bool
	logY   int
	logEl  *tui.Element

	devRows  map[int]*tui.Element // snap.Devicesの添字 -> 行要素
	ctrlRows map[int]*tui.Element // Propsの添字 -> 行要素
	ctrlEl   *tui.Element
	ctrlIdx  int
	ctrlY    int
	dialog   *dialog
	sent     map[string]string // デバイスID/dpCode -> 未反映の送信値

	cmd     lineEdit
	history []string
	histIdx int
	draft   string
	login   *loginForm

	snap     snapshot
	loaded   bool
	sel      string
	polledAt time.Time
	bootAt   time.Time
	running  string
	started  time.Time
	pending  int
}

func newConsoleUI(c *Client) *consoleUI {
	return &consoleUI{c: c, jobs: make(chan job, 16), follow: true, focus: paneControls, sent: map[string]string{}, bootAt: time.Now()}
}

func (m *consoleUI) BindApp(app *tui.App) { m.app = app }

func (m *consoleUI) Watchers() []tui.Watcher {
	return []tui.Watcher{tui.OnTimer(100*time.Millisecond, m.drain)}
}

func (m *consoleUI) drain() {
	m.in.mu.Lock()
	lines, snap := m.in.lines, m.in.snap
	m.in.lines, m.in.snap = nil, nil
	label, started, pending := m.in.label, m.in.started, m.in.pending
	m.in.mu.Unlock()
	if m.loaded && !m.snap.NoSession && time.Since(m.polledAt) >= pollInterval {
		m.polledAt = time.Now()
		m.poll()
	}
	if len(lines) == 0 && snap == nil && pending == 0 && m.pending == 0 {
		return
	}
	m.appendLines(lines...)
	if snap != nil {
		m.applySnapshot(*snap)
	}
	m.running, m.started, m.pending = label, started, pending
	m.app.MarkDirty()
}

func (m *consoleUI) appendLines(lines ...logLine) {
	m.lines = append(m.lines, lines...)
	if n := len(m.lines) - maxLogLines; n > 0 {
		m.lines = append([]logLine{}, m.lines[n:]...)
	}
}

func (m *consoleUI) applySnapshot(sn snapshot) {
	first := !m.loaded
	m.loaded = true
	m.snap = sn
	m.polledAt = time.Now()
	if !sn.poll {
		m.sent = map[string]string{}
	}
	if m.selected() == nil {
		id := ""
		for _, d := range sn.Devices {
			if id == "" || d.Online {
				id = d.ID
			}
			if d.Online {
				break
			}
		}
		m.selectDevice(id)
	}
	m.ctrlIdx = min(m.ctrlIdx, max(len(m.props())-1, 0))
	if first && sn.NoSession {
		m.openLogin("", "")
	}
}

func (m *consoleUI) selectDevice(id string) {
	if id == m.sel {
		return
	}
	m.sel = id
	m.ctrlIdx, m.ctrlY, m.dialog = 0, 0, nil
}

func (m *consoleUI) selectedIndex() int {
	for i := range m.snap.Devices {
		if m.snap.Devices[i].ID == m.sel {
			return i
		}
	}
	return -1
}

func (m *consoleUI) selected() *devView {
	if i := m.selectedIndex(); i >= 0 {
		return &m.snap.Devices[i]
	}
	return nil
}

func (m *consoleUI) props() []propView {
	if d := m.selected(); d != nil {
		return d.Props
	}
	return nil
}

func (m *consoleUI) targetDevice(args []string) *devView {
	key := flagValue(args, "id")
	if key == "" {
		key = flagValue(args, "name")
	}
	if key == "" {
		return m.selected()
	}
	for i := range m.snap.Devices {
		if d := &m.snap.Devices[i]; d.ID == key || d.Name == key {
			return d
		}
	}
	return nil
}

func (m *consoleUI) selectBy(delta int) {
	n := len(m.snap.Devices)
	if n == 0 {
		return
	}
	i := m.selectedIndex()
	if i < 0 {
		i = 0
	} else {
		i = (i + delta + n) % n
	}
	m.selectDevice(m.snap.Devices[i].ID)
}

func (m *consoleUI) ctrlMove(delta int) {
	m.ctrlIdx = min(max(m.ctrlIdx+delta, 0), max(len(m.props())-1, 0))
}

func (m *consoleUI) paneMove(delta int) {
	order := []pane{paneControls, paneCommand}
	if m.sideShown {
		order = []pane{paneDevices, paneControls, paneCommand}
	}
	for i, p := range order {
		if p == m.focus {
			m.focus = order[(i+delta+len(order))%len(order)]
			return
		}
	}
	m.focus = paneControls
}

// 未反映の送信値を優先
func (m *consoleUI) current(d *devView, code string) (string, bool) {
	if v, ok := m.sent[d.ID+"/"+code]; ok {
		return v, true
	}
	return d.Values[code], false
}

// boolは反転, enumは選択肢, 他は値入力
func (m *consoleUI) activate() {
	d, props := m.selected(), m.props()
	if d == nil || m.ctrlIdx >= len(props) {
		return
	}
	p := props[m.ctrlIdx]
	cur, _ := m.current(d, p.Code)
	switch {
	case p.Type == "bool":
		m.propose(p, cur != "true")
	case len(p.Values) > 0:
		dl := &dialog{prop: p, options: p.Values}
		for i, v := range p.Values {
			if v == cur {
				dl.idx = i
			}
		}
		m.dialog = dl
	default:
		dl := &dialog{prop: p, editing: true}
		dl.edit.set(cur)
		m.dialog = dl
	}
}

// riskyCodesは送信前に確認
func (m *consoleUI) propose(p propView, value interface{}) {
	d := m.selected()
	if d == nil {
		return
	}
	if riskyCodes[p.Code] {
		m.dialog = &dialog{prop: p, confirm: true, value: value}
		return
	}
	m.sendOp(d, p.Code, value)
}

func (m *consoleUI) dialogMove(delta int) {
	if n := len(m.dialog.options); n > 0 {
		m.dialog.idx = (m.dialog.idx + delta + n) % n
	}
}

func (m *consoleUI) dialogEnter() {
	dl := m.dialog
	switch {
	case dl.confirm:
		m.dialog = nil
		if d := m.selected(); d != nil {
			m.sendOp(d, dl.prop.Code, dl.value)
		}
	case dl.editing:
		text := strings.TrimSpace(dl.edit.String())
		var value interface{} = text
		if dl.prop.Type == "value" || dl.prop.Type == "bitmap" {
			f, err := strconv.ParseFloat(text, 64)
			if err != nil {
				dl.err = "invalid number"
				return
			}
			if dl.prop.Ranged && (f < dl.prop.Min || f > dl.prop.Max) {
				dl.err = "out of range"
				return
			}
			value = f
		}
		m.dialog = nil
		m.propose(dl.prop, value)
	default:
		m.dialog = nil
		m.propose(dl.prop, dl.options[dl.idx])
	}
}

func (m *consoleUI) editor() *lineEdit {
	switch {
	case !m.loaded:
	case m.login != nil:
		return &m.login.fields[m.login.active]
	case m.dialog != nil:
		if m.dialog.editing {
			return &m.dialog.edit
		}
	case m.focus == paneCommand:
		return &m.cmd
	}
	return nil
}

// 編集対象なし時はコマンド入力へ移動
func (m *consoleUI) typeRune(r rune) {
	if e := m.editor(); e != nil {
		e.insert(r)
		if m.dialog != nil {
			m.dialog.err = ""
		}
		return
	}
	if m.dialog != nil || !m.loaded {
		return
	}
	if r == ' ' {
		if m.focus == paneControls {
			m.activate()
		}
		return
	}
	m.focus = paneCommand
	m.cmd.insert(r)
}

func (m *consoleUI) openLogin(email, countryCode string) {
	f := &loginForm{}
	f.fields[0].set(email)
	f.fields[1].mask = true
	if countryCode == "" {
		countryCode = "1"
	}
	f.fields[2].set(countryCode)
	if email != "" {
		f.active = 1
	}
	m.login = f
}

func (m *consoleUI) loginMove(delta int) {
	n := len(m.login.fields)
	m.login.active = (m.login.active + delta + n) % n
}

func (m *consoleUI) loginEnter() {
	f := m.login
	email, password, cc := f.fields[0].String(), f.fields[1].String(), f.fields[2].String()
	switch {
	case email == "":
		f.active = 0
	case password == "":
		f.active = 1
	case cc == "":
		f.active = 2
	default:
		m.login = nil
		m.follow = true
		m.runCommand("auth login -email "+email, []string{"auth", "login", "-email", email, "-password", password, "-countryCode", cc})
	}
}

func (m *consoleUI) historyMove(delta int) {
	i := m.histIdx + delta
	if i < 0 || i > len(m.history) {
		return
	}
	if m.histIdx == len(m.history) {
		m.draft = m.cmd.String()
	}
	m.histIdx = i
	if i == len(m.history) {
		m.cmd.set(m.draft)
	} else {
		m.cmd.set(m.history[i])
	}
}

func (m *consoleUI) submit() {
	line := strings.TrimSpace(m.cmd.String())
	if line == "" {
		return
	}
	args := splitArgs(line)
	m.cmd.set("")
	m.follow = true
	echo := line
	if s, masked := maskPassword(args); masked {
		echo = s
	} else if len(m.history) == 0 || m.history[len(m.history)-1] != line {
		m.history = append(m.history, line)
	}
	m.histIdx = len(m.history)

	switch args[0] {
	case "quit", "exit", "q":
		m.app.Stop()
		return
	case "clear":
		m.lines = nil
		return
	case "help", "?":
		m.appendLines(logLine{kind: kindPrompt, text: echo})
		m.printHelp()
		return
	case "auth":
		if len(args) > 1 && args[1] == "login" && (flagValue(args, "email") == "" || flagValue(args, "password") == "") {
			if m.snap.LoggedIn {
				m.appendLines(logLine{kind: kindPrompt, text: echo}, logLine{kind: kindOK, text: "already logged in: " + m.snap.Email})
				m.refresh()
				return
			}
			m.openLogin(flagValue(args, "email"), flagValue(args, "countryCode"))
			return
		}
	}
	m.runCommand(echo, args)
}

func (m *consoleUI) printHelp() {
	m.appendLines(logLine{kind: kindHead, text: "commands:"})
	for _, c := range tuiCommands {
		m.appendLines(logLine{text: fmt.Sprintf("  %-24s %s", strings.TrimSpace(c.name+" "+c.args), c.desc)})
	}
	m.appendLines(
		logLine{kind: kindHead, text: "flags:"},
		logLine{text: "  --name X | --id ID       target device"},
		logLine{text: "  -force                   allow risky ops"},
		logLine{kind: kindHead, text: "keys:"},
		logLine{text: "  Tab/Shift+Tab            switch pane (complete in command)"},
		logLine{text: "  Up/Down                  move (history in command)"},
		logLine{text: "  Enter/Space              operate selected control"},
		logLine{text: "  Ctrl+N/Ctrl+P            select device"},
		logLine{text: "  PgUp/PgDn                scroll output"},
		logLine{text: "  Ctrl+R                   refresh devices"},
		logLine{text: "  Ctrl+L                   clear output"},
		logLine{text: "  Ctrl+A/E/U/K/W           line editing"},
		logLine{text: "  Ctrl+C                   quit"},
	)
}

func (m *consoleUI) scrollLog(delta int) {
	if m.logEl == nil {
		return
	}
	_, y := m.logEl.ScrollOffset()
	_, maxY := m.logEl.MaxScroll()
	m.logY = min(max(y+delta, 0), maxY)
	m.follow = m.logY >= maxY
}

func (m *consoleUI) pageSize() int {
	if m.logEl == nil {
		return 1
	}
	_, h := m.logEl.ViewportSize()
	return max(h-1, 1)
}

// nilは無処理
type keyActions struct {
	login, dialog, devices, controls, command func()
}

func (m *consoleUI) act(a keyActions) func() {
	return func() {
		fn := a.command
		switch {
		case !m.loaded:
			fn = nil
		case m.login != nil:
			fn = a.login
		case m.dialog != nil:
			fn = a.dialog
		case m.focus == paneDevices:
			fn = a.devices
		case m.focus == paneControls:
			fn = a.controls
		}
		if fn != nil {
			fn()
		}
	}
}

func (m *consoleUI) KeyMap() tui.KeyMap {
	on := func(k tui.KeyMatcher, fn func()) tui.KeyBinding {
		return tui.OnStop(k, func(tui.KeyEvent) {
			fn()
			m.app.MarkDirty()
		})
	}
	edit := func(fn func(e *lineEdit)) func() {
		return func() {
			if e := m.editor(); e != nil {
				fn(e)
			}
		}
	}
	panes := func(fn func()) func() {
		return m.act(keyActions{devices: fn, controls: fn, command: fn})
	}
	return tui.KeyMap{
		tui.OnStop(tui.KeyCtrlC, func(ke tui.KeyEvent) { ke.App().Stop() }),
		tui.OnStop(tui.AnyRune, func(ke tui.KeyEvent) {
			m.typeRune(ke.Rune)
			m.app.MarkDirty()
		}),
		on(tui.KeyBackspace, edit((*lineEdit).backspace)),
		on(tui.KeyDelete, edit((*lineEdit).del)),
		on(tui.KeyLeft, edit(func(e *lineEdit) { e.moveTo(e.pos - 1) })),
		on(tui.KeyRight, edit(func(e *lineEdit) { e.moveTo(e.pos + 1) })),
		on(tui.KeyHome, edit(func(e *lineEdit) { e.moveTo(0) })),
		on(tui.KeyEnd, edit(func(e *lineEdit) { e.moveTo(len(e.runes)) })),
		on(tui.KeyCtrlA, edit(func(e *lineEdit) { e.moveTo(0) })),
		on(tui.KeyCtrlE, edit(func(e *lineEdit) { e.moveTo(len(e.runes)) })),
		on(tui.KeyCtrlU, edit((*lineEdit).killBefore)),
		on(tui.KeyCtrlK, edit((*lineEdit).killAfter)),
		on(tui.KeyCtrlW, edit((*lineEdit).killWord)),
		on(tui.KeyEnter, m.act(keyActions{
			login:    m.loginEnter,
			dialog:   m.dialogEnter,
			devices:  func() { m.focus = paneControls },
			controls: m.activate,
			command:  m.submit,
		})),
		on(tui.KeyTab, m.act(keyActions{
			login:    func() { m.loginMove(1) },
			devices:  func() { m.paneMove(1) },
			controls: func() { m.paneMove(1) },
			command: func() {
				if len(m.cmd.runes) == 0 {
					m.paneMove(1)
				} else {
					m.complete()
				}
			},
		})),
		on(tui.KeyTab.Shift(), m.act(keyActions{
			login:    func() { m.loginMove(-1) },
			devices:  func() { m.paneMove(-1) },
			controls: func() { m.paneMove(-1) },
			command:  func() { m.paneMove(-1) },
		})),
		on(tui.KeyUp, m.act(keyActions{
			login:    func() { m.loginMove(-1) },
			dialog:   func() { m.dialogMove(-1) },
			devices:  func() { m.selectBy(-1) },
			controls: func() { m.ctrlMove(-1) },
			command:  func() { m.historyMove(-1) },
		})),
		on(tui.KeyDown, m.act(keyActions{
			login:    func() { m.loginMove(1) },
			dialog:   func() { m.dialogMove(1) },
			devices:  func() { m.selectBy(1) },
			controls: func() { m.ctrlMove(1) },
			command:  func() { m.historyMove(1) },
		})),
		on(tui.KeyEscape, m.act(keyActions{
			login:  func() { m.login = nil },
			dialog: func() { m.dialog = nil },
			command: func() {
				if len(m.cmd.runes) == 0 {
					m.focus = paneControls
				} else {
					m.cmd.set("")
				}
			},
		})),
		on(tui.KeyPageUp, panes(func() { m.scrollLog(-m.pageSize()) })),
		on(tui.KeyPageDown, panes(func() { m.scrollLog(m.pageSize()) })),
		on(tui.KeyCtrlN, panes(func() { m.selectBy(1) })),
		on(tui.KeyCtrlP, panes(func() { m.selectBy(-1) })),
		on(tui.KeyCtrlR, m.act(keyActions{login: m.refresh, dialog: m.refresh, devices: m.refresh, controls: m.refresh, command: m.refresh})),
		on(tui.KeyCtrlL, func() { m.lines = nil }),
	}
}

func (m *consoleUI) HandleMouse(me tui.MouseEvent) bool {
	if !m.loaded || m.login != nil || m.dialog != nil {
		return false
	}
	switch me.Button {
	case tui.MouseWheelUp, tui.MouseWheelDown:
		delta := 1
		if me.Button == tui.MouseWheelUp {
			delta = -1
		}
		switch {
		case m.logEl != nil && m.logEl.Rect().Contains(me.X, me.Y):
			m.scrollLog(3 * delta)
		case m.ctrlEl != nil && m.ctrlEl.Rect().Contains(me.X, me.Y):
			m.ctrlMove(delta)
		default:
			return false
		}
		m.app.MarkDirty()
		return true
	case tui.MouseLeft:
		if me.Action != tui.MousePress {
			return false
		}
		for i, el := range m.devRows {
			if el.ContainsPoint(me.X, me.Y) {
				m.selectDevice(m.snap.Devices[i].ID)
				m.focus = paneDevices
				m.app.MarkDirty()
				return true
			}
		}
		for i, el := range m.ctrlRows {
			if el.ContainsPoint(me.X, me.Y) {
				// 選択済みの行のクリックで操作
				if m.focus == paneControls && i == m.ctrlIdx {
					m.activate()
				}
				m.focus, m.ctrlIdx = paneControls, i
				m.app.MarkDirty()
				return true
			}
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func formatNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func row(opts ...tui.Option) *tui.Element {
	return tui.New(append([]tui.Option{tui.WithDisplay(tui.DisplayFlex), tui.WithDirection(tui.Row)}, opts...)...)
}

func column(opts ...tui.Option) *tui.Element {
	return tui.New(append([]tui.Option{tui.WithDisplay(tui.DisplayFlex), tui.WithDirection(tui.Column)}, opts...)...)
}

func panel(title string, border tui.Style, opts ...tui.Option) *tui.Element {
	return column(append([]tui.Option{
		tui.WithBorder(tui.BorderRounded),
		tui.WithBorderStyle(border),
		tui.WithBorderTitle(title),
		tui.WithBorderTitleAlign(tui.TextAlignLeft),
	}, opts...)...)
}

func line(opts ...tui.Option) *tui.Element {
	return tui.New(append([]tui.Option{tui.WithHeight(1), tui.WithWrap(false)}, opts...)...)
}

func (m *consoleUI) borderFor(p pane) tui.Style {
	if m.focus == p {
		return titleStyle
	}
	return dimStyle
}

func (m *consoleUI) renderHeader() *tui.Element {
	h := row(tui.WithHeight(1), tui.WithJustify(tui.JustifySpaceBetween))
	h.AddChild(line(tui.WithText("petsnowy-cli"), tui.WithTextStyle(titleStyle)))
	switch {
	case m.snap.Err != "":
		h.AddChild(line(tui.WithText(truncate(m.snap.Err, 60)), tui.WithTextStyle(errStyle)))
	case m.snap.LoggedIn:
		h.AddChild(line(tui.WithRichText(tui.TextSpan{Text: "● ", Style: okStyle}, tui.TextSpan{Text: m.snap.Email})))
	case m.loaded:
		h.AddChild(line(tui.WithText("○ not logged in"), tui.WithTextStyle(dimStyle)))
	}
	return h
}

func (m *consoleUI) renderSide(width int) *tui.Element {
	side := column(tui.WithWidth(width))

	devs := m.snap.Devices
	start := min(max(m.selectedIndex()-maxDeviceRows+1, 0), max(len(devs)-maxDeviceRows, 0))
	end := min(start+maxDeviceRows, len(devs))
	list := panel("Devices", m.borderFor(paneDevices), tui.WithHeight(max(end-start, 1)+2))
	for i := start; i < end; i++ {
		d := devs[i]
		cursor, name := tui.TextSpan{Text: "  "}, tui.TextSpan{Text: d.Name}
		if d.ID == m.sel {
			cursor, name.Style = tui.TextSpan{Text: "▸ ", Style: titleStyle}, boldStyle
		}
		mark := tui.TextSpan{Text: "○ ", Style: dimStyle}
		if d.Online {
			mark = tui.TextSpan{Text: "● ", Style: okStyle}
		}
		el := line(tui.WithRichText(cursor, mark, name))
		m.devRows[i] = el
		list.AddChild(el)
	}
	side.AddChild(list)

	status := panel("Status", dimStyle,
		tui.WithFlexGrow(1),
		tui.WithScrollable(tui.ScrollVertical),
		tui.WithScrollbarStyle(dimStyle),
	)
	if d := m.selected(); d != nil {
		for _, dp := range d.Status {
			style := tui.NewStyle()
			switch dp.Value {
			case "true":
				style = okStyle
			case "false":
				style = dimStyle
			}
			r := row(tui.WithHeight(1))
			r.AddChild(line(tui.WithText(dp.Code), tui.WithFlexGrow(1)))
			r.AddChild(line(tui.WithText(" "+truncate(dp.Value, 14)), tui.WithTextStyle(style)))
			status.AddChild(r)
		}
	}
	side.AddChild(status)
	return side
}

func (m *consoleUI) valueSpans(d *devView, p propView) []tui.TextSpan {
	v, unsynced := m.current(d, p.Code)
	var spans []tui.TextSpan
	switch {
	case v == "":
		spans = []tui.TextSpan{{Text: "-", Style: dimStyle}}
	case p.Type == "bool" && v == "true":
		spans = []tui.TextSpan{{Text: "● ON", Style: okStyle}}
	case p.Type == "bool":
		spans = []tui.TextSpan{{Text: "○ OFF", Style: dimStyle}}
	default:
		spans = []tui.TextSpan{{Text: truncate(v, 24)}}
		if p.Unit != "" {
			spans = append(spans, tui.TextSpan{Text: " " + p.Unit, Style: dimStyle})
		}
	}
	if unsynced {
		spans = append(spans, tui.TextSpan{Text: " …", Style: warnStyle})
	}
	return spans
}

// viewport: 前フレームの表示行数(未確定時は0)
func (m *consoleUI) renderControls(width, viewport int) *tui.Element {
	d, props := m.selected(), m.props()
	focused := m.focus == paneControls
	codeWidth := 0
	for _, p := range props {
		codeWidth = min(max(codeWidth, len(p.Code)), maxCodeWidth)
	}
	indent := strings.Repeat(" ", 2+codeWidth+2)
	// 枠線2桁とスクロールバー1桁を除外
	valueWidth := max(width-3-len(indent), 4)

	var rows []*tui.Element
	extra := 0
	for i, p := range props {
		selected := i == m.ctrlIdx
		cursor := tui.TextSpan{Text: "  "}
		codeStyle := tui.NewStyle()
		if riskyCodes[p.Code] {
			codeStyle = warnStyle
		}
		if selected {
			cursor = tui.TextSpan{Text: "▸ ", Style: dimStyle}
			if focused {
				cursor.Style, codeStyle = titleStyle, codeStyle.Bold()
			}
		}
		spans := []tui.TextSpan{cursor, {Text: fmt.Sprintf("%-*s  ", codeWidth, truncate(p.Code, maxCodeWidth)), Style: codeStyle}}
		dl := m.dialog
		if !selected {
			dl = nil
		}
		if dl != nil && dl.editing {
			spans = append(spans, dl.edit.spans(valueWidth, true)...)
		} else {
			spans = append(spans, m.valueSpans(d, p)...)
		}
		el := line(tui.WithRichText(spans...))
		m.ctrlRows[i] = el
		rows = append(rows, el)
		if dl == nil {
			continue
		}
		before := len(rows)
		switch {
		case dl.confirm:
			rows = append(rows, line(tui.WithText(fmt.Sprintf("%ssend %s=%v ?", indent, p.Code, dl.value)), tui.WithTextStyle(warnStyle.Bold())))
		case dl.editing:
			if dl.err != "" {
				rows = append(rows, line(tui.WithText(indent+dl.err), tui.WithTextStyle(errStyle)))
			}
		default:
			for j, o := range dl.options {
				mark, style := tui.TextSpan{Text: "  "}, tui.NewStyle()
				if j == dl.idx {
					mark, style = tui.TextSpan{Text: "▸ ", Style: titleStyle}, boldStyle
				}
				rows = append(rows, line(tui.WithRichText(tui.TextSpan{Text: indent}, mark, tui.TextSpan{Text: o, Style: style})))
			}
		}
		extra = len(rows) - before
	}

	if viewport > 0 {
		if bottom := m.ctrlIdx + extra; bottom >= m.ctrlY+viewport {
			m.ctrlY = bottom - viewport + 1
		}
		m.ctrlY = min(m.ctrlY, m.ctrlIdx)
	}
	box := panel("Controls", m.borderFor(paneControls),
		tui.WithFlexGrow(1),
		tui.WithScrollable(tui.ScrollVertical),
		tui.WithScrollbarStyle(dimStyle),
		tui.WithScrollOffset(0, m.ctrlY),
	)
	box.AddChild(rows...)
	m.ctrlEl = box
	return box
}

// 空白を保持する表示幅単位の折り返し
func wrapSpans(spans []tui.TextSpan, width int) [][]tui.TextSpan {
	var rows [][]tui.TextSpan
	var cur []tui.TextSpan
	w := 0
	for _, sp := range spans {
		var seg []rune
		flush := func() {
			if len(seg) > 0 {
				cur = append(cur, tui.TextSpan{Text: string(seg), Style: sp.Style})
				seg = nil
			}
		}
		for _, r := range sp.Text {
			rw := tui.StringWidth(string(r))
			if w+rw > width && w > 0 {
				flush()
				rows = append(rows, cur)
				cur, w = nil, 0
			}
			seg = append(seg, r)
			w += rw
		}
		flush()
	}
	return append(rows, cur)
}

func (l logLine) spans() []tui.TextSpan {
	if l.kind == kindPrompt {
		spans := []tui.TextSpan{{Text: promptCh, Style: titleStyle}, {Text: l.text, Style: boldStyle}}
		if l.target != "" {
			spans = append(spans, tui.TextSpan{Text: "  " + l.target, Style: dimStyle})
		}
		return spans
	}
	style := tui.NewStyle()
	switch l.kind {
	case kindHead:
		style = boldStyle
	case kindOK:
		style = okStyle
	case kindWarn:
		style = warnStyle
	case kindErr:
		style = errStyle
	}
	return []tui.TextSpan{{Text: l.text, Style: style}}
}

func (m *consoleUI) renderLog(width, height int) *tui.Element {
	title := "Output"
	if m.pending > 0 {
		label := m.running
		if label == "" {
			label = "waiting"
		}
		elapsed := time.Since(m.started)
		title = fmt.Sprintf("%c %s %ds", spinner[int(elapsed/(100*time.Millisecond))%len(spinner)], label, int(elapsed.Seconds()))
	}
	y := m.logY
	if m.follow {
		y = 1 << 30
	}
	box := panel(title, dimStyle,
		tui.WithHeight(height),
		tui.WithScrollable(tui.ScrollVertical),
		tui.WithScrollbarStyle(dimStyle),
		tui.WithScrollOffset(0, y),
	)
	// 枠線2桁とスクロールバー1桁を除外
	inner := max(width-3, 1)
	for _, l := range m.lines {
		for _, r := range wrapSpans(l.spans(), inner) {
			if len(r) == 0 {
				r = []tui.TextSpan{{Text: " "}}
			}
			box.AddChild(line(tui.WithRichText(r...)))
		}
	}
	m.logEl = box
	return box
}

func (m *consoleUI) renderInput(width int) *tui.Element {
	active := m.focus == paneCommand
	promptStyle := dimStyle
	if active {
		promptStyle = titleStyle
	}
	var prompt []tui.TextSpan
	promptWidth := tui.StringWidth(promptCh)
	if d := m.selected(); d != nil {
		name := "[" + truncate(d.Name, 20) + "] "
		prompt = append(prompt, tui.TextSpan{Text: name, Style: dimStyle})
		promptWidth += tui.StringWidth(name)
	}
	prompt = append(prompt, tui.TextSpan{Text: promptCh, Style: promptStyle})
	r := row(tui.WithHeight(1))
	r.AddChild(line(tui.WithRichText(prompt...), tui.WithWidth(promptWidth)))
	r.AddChild(line(tui.WithRichText(m.cmd.spans(width-promptWidth, active)...), tui.WithFlexGrow(1)))
	return r
}

func propHint(p propView) string {
	switch {
	case len(p.Values) > 0:
		return p.Type + ": " + strings.Join(p.Values, " | ")
	case p.Ranged:
		s := fmt.Sprintf("%s: %s..%s", p.Type, formatNum(p.Min), formatNum(p.Max))
		if p.Step != 0 {
			s += " step " + formatNum(p.Step)
		}
		return strings.TrimSpace(s + " " + p.Unit)
	}
	return p.Type
}

func (m *consoleUI) hint() string {
	switch m.focus {
	case paneDevices:
		if d := m.selected(); d != nil {
			return "id: " + d.ID
		}
		return ""
	case paneControls:
		if props := m.props(); m.ctrlIdx < len(props) {
			return propHint(props[m.ctrlIdx])
		}
		return ""
	}
	text := m.cmd.String()
	partial, cands := m.candidates(text)
	if len(cands) > 0 && !(len(cands) == 1 && cands[0] == partial) {
		return strings.Join(cands, "  ")
	}
	if toks := strings.Fields(text); len(toks) > 0 {
		if s := findCommand(toks[0]); s != nil {
			return strings.TrimSpace(s.name+" "+s.args) + "  " + s.desc
		}
	}
	return ""
}

func (m *consoleUI) keyHelp() string {
	switch {
	case !m.loaded:
		return "Ctrl+C quit"
	case m.login != nil:
		return "Enter next/submit   Tab switch field   Esc cancel   Ctrl+C quit"
	case m.dialog != nil && m.dialog.editing:
		return "Enter send   Esc cancel"
	case m.dialog != nil && m.dialog.confirm:
		return "Enter send   Esc cancel"
	case m.dialog != nil:
		return "Up/Down choose   Enter send   Esc cancel"
	case m.focus == paneDevices:
		return "Up/Down select   Enter controls   Tab pane   Ctrl+R refresh   Ctrl+C quit"
	case m.focus == paneControls:
		return "Up/Down move   Enter operate   Tab pane   Ctrl+N/P device   Ctrl+R refresh   Ctrl+C quit"
	}
	return "Tab complete   Up/Down history   Esc controls   PgUp/PgDn scroll   Ctrl+L clear   Ctrl+C quit"
}

func (m *consoleUI) renderLogin() *tui.Element {
	const labelWidth, fieldWidth = 14, 34
	box := panel("Login", titleStyle,
		tui.WithWidth(labelWidth+fieldWidth+4),
		tui.WithPaddingTRBL(0, 1, 0, 1),
	)
	for i := range m.login.fields {
		style := dimStyle
		if i == m.login.active {
			style = boldStyle
		}
		r := row(tui.WithHeight(1))
		r.AddChild(line(tui.WithText(loginLabels[i]), tui.WithTextStyle(style), tui.WithWidth(labelWidth)))
		r.AddChild(line(tui.WithRichText(m.login.fields[i].spans(fieldWidth, i == m.login.active)...), tui.WithFlexGrow(1)))
		box.AddChild(r)
	}
	wrap := column(tui.WithFlexGrow(1), tui.WithJustify(tui.JustifyCenter), tui.WithAlign(tui.AlignCenter))
	wrap.AddChild(box)
	return wrap
}

func (m *consoleUI) renderLoading() *tui.Element {
	frame := spinner[int(time.Since(m.bootAt)/(100*time.Millisecond))%len(spinner)]
	wrap := column(tui.WithFlexGrow(1), tui.WithJustify(tui.JustifyCenter), tui.WithAlign(tui.AlignCenter))
	wrap.AddChild(line(tui.WithRichText(
		tui.TextSpan{Text: string(frame) + " ", Style: titleStyle},
		tui.TextSpan{Text: "Loading devices"},
	)))
	return wrap
}

func (m *consoleUI) Render(app *tui.App) *tui.Element {
	width, height := app.Size()
	viewport := 0
	if m.ctrlEl != nil {
		_, viewport = m.ctrlEl.ViewportSize()
	}
	m.devRows, m.ctrlRows = map[int]*tui.Element{}, map[int]*tui.Element{}
	m.logEl, m.ctrlEl = nil, nil

	root := column(tui.WithWidthPercent(100), tui.WithHeightPercent(100))
	root.AddChild(m.renderHeader())
	if !m.loaded {
		root.AddChild(m.renderLoading())
	} else if m.login != nil {
		root.AddChild(m.renderLogin())
	} else {
		// 固定高の子孫を含む時の伸長不可回避
		body := row(tui.WithHeight(0), tui.WithFlexGrow(1))
		side := 0
		m.sideShown = width >= sideMinWidth
		if m.sideShown {
			side = min(max(width*35/100, 30), 44)
			body.AddChild(m.renderSide(side))
		} else if m.focus == paneDevices {
			m.focus = paneControls
		}
		main := column(tui.WithFlexGrow(1))
		main.AddChild(m.renderControls(width-side, viewport))
		main.AddChild(m.renderLog(width-side, min(max(height/3, 5), 12)))
		body.AddChild(main)
		root.AddChild(body)
		root.AddChild(m.renderInput(width))
		root.AddChild(line(tui.WithText(truncate(m.hint(), max(width, 1))), tui.WithTextStyle(dimStyle)))
	}
	root.AddChild(line(tui.WithText(truncate(m.keyHelp(), max(width, 1))), tui.WithTextStyle(dimStyle)))
	return root
}

// TUI起動(非TTY時はREPLへフォールバック)
func runTUI() {
	m := newConsoleUI(NewClient(randomHex(8)))
	app, err := tui.NewApp(
		tui.WithRootComponent(m),
		tui.WithFrameRate(30),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI failed:", err)
		interactive()
		return
	}
	defer app.Close()
	tuiActive = true
	cliOut = &m.in
	go m.work()
	m.refresh()
	err = app.Run()
	tuiActive = false
	cliOut = os.Stdout
	if err != nil {
		fmt.Fprintln(os.Stderr, "TUI failed:", err)
		interactive()
	}
}
