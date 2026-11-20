package main

// TUI Worker: API直列実行, UIへの結果受け渡し

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type lineKind int

const (
	kindPlain lineKind = iota
	kindPrompt
	kindHead
	kindOK
	kindWarn
	kindErr
)

type logLine struct {
	kind   lineKind
	text   string
	target string // kindPrompt時の対象デバイス名
}

func classify(s string) lineKind {
	t := strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(t, "error:"), strings.HasPrefix(t, "unknown command:"), strings.HasSuffix(t, "decode error"):
		return kindErr
	case strings.HasPrefix(t, "warn:"), strings.HasPrefix(t, "usage:"), strings.HasPrefix(t, "not logged in"), strings.HasPrefix(t, "session expired"):
		return kindWarn
	case strings.HasPrefix(t, "op ok"), strings.HasPrefix(t, "send ok"), strings.HasPrefix(t, "login ok"), strings.HasPrefix(t, "logged in"), strings.HasPrefix(t, "already logged in"), strings.HasPrefix(t, "logged out"):
		return kindOK
	case strings.HasPrefix(s, "home:"), strings.HasPrefix(s, "device:"):
		return kindHead
	}
	return kindPlain
}

type dpView struct {
	Code  string
	Value string
}

type propView struct {
	Code   string
	Type   string
	Values []string // enumの選択肢
	Min    float64
	Max    float64
	Step   float64
	Ranged bool
	Unit   string
}

type devView struct {
	ID     string
	Name   string
	Online bool
	Props  []propView        // rwのみ
	Status []dpView          // rw以外のdp
	Values map[string]string // dpCode -> 現在値
}

type snapshot struct {
	poll      bool // 定期取得による更新
	homes     []homeEntry
	NoSession bool
	LoggedIn  bool
	Email     string
	Err       string
	Devices   []devView
}

func dpValue(v interface{}) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case string:
		return x
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func viewProp(code string, p dpProp) propView {
	pv := propView{Code: code}
	pv.Type, _ = p.TypeSpec["type"].(string)
	pv.Unit, _ = p.TypeSpec["unit"].(string)
	if r, ok := p.TypeSpec["range"].([]interface{}); ok {
		for _, x := range r {
			pv.Values = append(pv.Values, fmt.Sprint(x))
		}
	}
	lo, okLo := p.TypeSpec["min"].(float64)
	hi, okHi := p.TypeSpec["max"].(float64)
	if okLo && okHi {
		pv.Min, pv.Max, pv.Ranged = lo, hi, true
	}
	pv.Step, _ = p.TypeSpec["step"].(float64)
	return pv
}

func viewDevice(d deviceEntry, pm *productModel) devView {
	dv := devView{ID: d.DeviceID, Name: d.Name, Online: d.CloudOnline, Values: map[string]string{}}
	idToCode := map[string]string{}
	rw := map[string]bool{}
	if pm != nil {
		for code, p := range pm.Props {
			idToCode[strconv.Itoa(p.AbilityID)] = code
			if p.AccessMode == "rw" {
				rw[code] = true
				dv.Props = append(dv.Props, viewProp(code, p))
			}
		}
	}
	sort.Slice(dv.Props, func(i, j int) bool { return dv.Props[i].Code < dv.Props[j].Code })
	var dpi struct {
		Dps map[string]interface{} `json:"dps"`
	}
	if len(d.DataPointInfo) > 0 && json.Unmarshal(d.DataPointInfo, &dpi) == nil {
		for id, v := range dpi.Dps {
			code, ok := idToCode[id]
			if !ok {
				code = id
			}
			dv.Values[code] = dpValue(v)
			if !rw[code] {
				dv.Status = append(dv.Status, dpView{Code: code, Value: dpValue(v)})
			}
		}
	}
	sort.Slice(dv.Status, func(i, j int) bool { return dv.Status[i].Code < dv.Status[j].Code })
	return dv
}

func (c *Client) modelFor(d deviceEntry) *productModel {
	if d.ProductID == "" {
		return nil
	}
	if pm, ok := c.Models[d.ProductID]; ok {
		return pm
	}
	raw, err := c.thingModel(d.ProductID, d.ProductVer)
	if err != nil {
		return nil
	}
	pm := parseModel(raw)
	c.Models[d.ProductID] = pm
	return pm
}

func (c *Client) snapshot() snapshot {
	var sn snapshot
	s, err := loadSession()
	if err != nil {
		sn.NoSession = true
		return sn
	}
	sd, err := c.sessionValid(s.SID)
	if err != nil {
		sn.Err = "session check failed: " + err.Error()
		return sn
	}
	sn.LoggedIn, sn.Email = true, sd.Email
	homes, err := c.homes()
	if err != nil {
		sn.Err = err.Error()
		return sn
	}
	sn.homes = homes
	if sn.Devices, err = c.deviceViews(homes); err != nil {
		sn.Err = err.Error()
	}
	return sn
}

func (c *Client) deviceViews(homes []homeEntry) ([]devView, error) {
	var views []devView
	for _, h := range homes {
		devs, err := c.devices(h.GroupID)
		if err != nil {
			return views, err
		}
		for _, d := range devs {
			views = append(views, viewDevice(d, c.modelFor(d)))
		}
	}
	return views, nil
}

// cliOutの書き込み先を兼ねる
type inbox struct {
	mu      sync.Mutex
	lines   []logLine
	partial string
	snap    *snapshot
	label   string
	started time.Time
	pending int
	polling bool
}

func (b *inbox) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	parts := strings.Split(b.partial+string(p), "\n")
	b.partial = parts[len(parts)-1]
	for _, s := range parts[:len(parts)-1] {
		b.lines = append(b.lines, logLine{kind: classify(s), text: s})
	}
	return len(p), nil
}

func (b *inbox) push(l logLine) {
	b.mu.Lock()
	b.lines = append(b.lines, l)
	b.mu.Unlock()
}

type job struct {
	label string
	quiet bool // 実行中表示と待ち件数の対象外
	fn    func()
}

func (m *consoleUI) enqueue(label string, fn func()) {
	m.in.mu.Lock()
	m.in.pending++
	m.in.mu.Unlock()
	select {
	case m.jobs <- job{label: label, fn: fn}:
	default:
		m.in.mu.Lock()
		m.in.pending--
		m.in.mu.Unlock()
		m.in.push(logLine{kind: kindErr, text: "error: command queue is full"})
	}
}

// Client並行利用不可
func (m *consoleUI) work() {
	for j := range m.jobs {
		if !j.quiet {
			m.in.mu.Lock()
			m.in.label, m.in.started = j.label, time.Now()
			m.in.mu.Unlock()
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(&m.in, "error: command aborted: %v\n", r)
				}
			}()
			j.fn()
		}()
		m.in.mu.Lock()
		if m.in.partial != "" {
			m.in.lines = append(m.in.lines, logLine{kind: classify(m.in.partial), text: m.in.partial})
			m.in.partial = ""
		}
		if j.quiet {
			m.in.polling = false
		} else {
			m.in.label = ""
			m.in.pending--
		}
		m.in.mu.Unlock()
	}
}

// 他ジョブの実行中・待機中は取得対象外
func (m *consoleUI) poll() {
	prev := m.snap
	m.in.mu.Lock()
	busy := m.in.polling || m.in.pending > 0
	if !busy {
		m.in.polling = true
	}
	m.in.mu.Unlock()
	if busy {
		return
	}
	fn := func() {
		sn := prev
		sn.poll = true
		err := fmt.Errorf("not logged in")
		if prev.LoggedIn && m.c.SID != "" {
			sn.Devices, err = m.c.deviceViews(prev.homes)
			sn.Err = ""
		}
		// 失敗時はセッション確認から再取得
		if err != nil {
			sn = m.c.snapshot()
			sn.poll = true
		}
		m.in.mu.Lock()
		m.in.snap = &sn
		m.in.mu.Unlock()
	}
	select {
	case m.jobs <- job{quiet: true, fn: fn}:
	default:
		m.in.mu.Lock()
		m.in.polling = false
		m.in.mu.Unlock()
	}
}

func (m *consoleUI) refresh() { m.refreshAfter(0) }

func (m *consoleUI) refreshAfter(delay time.Duration) {
	m.enqueue("refresh", func() {
		time.Sleep(delay)
		sn := m.c.snapshot()
		m.in.mu.Lock()
		m.in.snap = &sn
		m.in.mu.Unlock()
	})
}

// パネル操作によるdp送信
func (m *consoleUI) sendOp(dev *devView, code string, value interface{}) {
	id, name := dev.ID, dev.Name
	m.sent[id+"/"+code] = dpValue(value)
	m.follow = true
	m.enqueue("op", func() {
		m.in.push(logLine{kind: kindPrompt, text: fmt.Sprintf("op %s %v", code, value), target: name})
		raw, err := m.c.sendByCode(id, code, value)
		if err != nil {
			fmt.Fprintln(&m.in, "error:", err)
			return
		}
		fmt.Fprintf(&m.in, "op ok: %s=%v -> %s\n", code, value, string(raw))
	})
	m.refreshAfter(time.Second)
}

func (m *consoleUI) runCommand(echo string, args []string) {
	spec := findCommand(args[0])
	target := ""
	if spec != nil && spec.device && !hasDeviceFlag(args) {
		if d := m.selected(); d != nil {
			args = append(args, "--id", d.ID)
			target = d.Name
		}
	}
	m.enqueue(args[0], func() {
		m.in.push(logLine{kind: kindPrompt, text: echo, target: target})
		run(args, m.c)
	})
	if spec == nil || spec.mutate {
		m.refresh()
	}
}
