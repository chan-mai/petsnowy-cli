package main

// TUI入力: 行編集, 引数分割, 補完候補

import (
	"sort"
	"strings"

	tui "github.com/grindlemire/go-tui"
)

type lineEdit struct {
	runes []rune
	pos   int
	mask  bool
}

func (e *lineEdit) String() string { return string(e.runes) }

func (e *lineEdit) set(s string) {
	e.runes = []rune(s)
	e.pos = len(e.runes)
}

func (e *lineEdit) insert(r rune) {
	e.runes = append(e.runes[:e.pos], append([]rune{r}, e.runes[e.pos:]...)...)
	e.pos++
}

func (e *lineEdit) backspace() {
	if e.pos > 0 {
		e.runes = append(e.runes[:e.pos-1], e.runes[e.pos:]...)
		e.pos--
	}
}

func (e *lineEdit) del() {
	if e.pos < len(e.runes) {
		e.runes = append(e.runes[:e.pos], e.runes[e.pos+1:]...)
	}
}

func (e *lineEdit) moveTo(pos int) {
	e.pos = min(max(pos, 0), len(e.runes))
}

func (e *lineEdit) killBefore() {
	e.runes = append([]rune{}, e.runes[e.pos:]...)
	e.pos = 0
}

func (e *lineEdit) killAfter() {
	e.runes = e.runes[:e.pos]
}

func (e *lineEdit) killWord() {
	i := e.pos
	for i > 0 && e.runes[i-1] == ' ' {
		i--
	}
	for i > 0 && e.runes[i-1] != ' ' {
		i--
	}
	e.runes = append(e.runes[:i], e.runes[e.pos:]...)
	e.pos = i
}

func (e *lineEdit) spans(width int, active bool) []tui.TextSpan {
	shown := e.runes
	if e.mask {
		shown = []rune(strings.Repeat("*", len(e.runes)))
	}
	start := 0
	for start < e.pos && tui.StringWidth(string(shown[start:e.pos])) > width-1 {
		start++
	}
	before := string(shown[start:e.pos])
	cur, rest := " ", []rune{}
	if e.pos < len(shown) {
		cur, rest = string(shown[e.pos]), shown[e.pos+1:]
	}
	room := width - tui.StringWidth(before) - tui.StringWidth(cur)
	end := 0
	for end < len(rest) && tui.StringWidth(string(rest[:end+1])) <= room {
		end++
	}
	curStyle := tui.NewStyle()
	if active {
		curStyle = curStyle.Reverse()
	}
	return []tui.TextSpan{
		{Text: before},
		{Text: cur, Style: curStyle},
		{Text: string(rest[:end])},
	}
}

// トークン先頭の引用符のみ引用扱い
func splitArgs(s string) []string {
	var out []string
	var cur []rune
	var quote rune
	started := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur = append(cur, r)
			}
		case r == ' ' || r == '\t':
			if started {
				out = append(out, string(cur))
				cur, started = nil, false
			}
		case !started && (r == '"' || r == '\''):
			quote, started = r, true
		default:
			cur = append(cur, r)
			started = true
		}
	}
	if started {
		out = append(out, string(cur))
	}
	return out
}

type cmdSpec struct {
	name   string
	args   string
	desc   string
	device bool // デバイス指定必須
	mutate bool // 実行後に状態再取得
}

var tuiCommands = []cmdSpec{
	{name: "devices", desc: "list devices"},
	{name: "state", desc: "show dp state", device: true},
	{name: "model", desc: "show dpCode/dpID table", device: true},
	{name: "op", args: "<dpCode> <value>", desc: "operate by dpCode", device: true, mutate: true},
	{name: "clean", desc: "manual clean", device: true, mutate: true},
	{name: "feed", args: "[n]", desc: "manual feed", device: true, mutate: true},
	{name: "light", args: "on|off", desc: "light switch", device: true, mutate: true},
	{name: "sleep", args: "on|off", desc: "sleep mode", device: true, mutate: true},
	{name: "send", args: "<dpsJson>", desc: "raw dp send", device: true, mutate: true},
	{name: "auth", args: "login|status|logout", desc: "session", mutate: true},
	{name: "time", desc: "server time"},
	{name: "help", desc: "show commands and keys"},
	{name: "clear", desc: "clear output"},
	{name: "quit", desc: "exit"},
}

func findCommand(name string) *cmdSpec {
	for i := range tuiCommands {
		if tuiCommands[i].name == name {
			return &tuiCommands[i]
		}
	}
	return nil
}

func isDeviceFlag(a string) bool {
	return a == "-name" || a == "--name" || a == "-id" || a == "--id"
}

func hasDeviceFlag(args []string) bool {
	for _, a := range args {
		if isDeviceFlag(a) {
			return true
		}
	}
	return false
}

func flagValue(args []string, key string) string {
	for i, a := range args {
		name, val, eq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != key {
			continue
		}
		if eq {
			return val
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func maskPassword(args []string) (string, bool) {
	out := make([]string, len(args))
	masked := false
	for i, a := range args {
		name, _, eq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case strings.HasPrefix(a, "-") && name == "password" && eq:
			a = a[:strings.Index(a, "=")+1] + "****"
			masked = true
		case i > 0 && (args[i-1] == "-password" || args[i-1] == "--password"):
			a = "****"
			masked = true
		}
		out[i] = quoteArg(a)
	}
	return strings.Join(out, " "), masked
}

func positionals(args []string) []string {
	var out []string
	for i := 1; i < len(args); i++ {
		if isDeviceFlag(args[i]) {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// 戻り値: 入力途中の末尾トークン, 補完候補
func (m *consoleUI) candidates(line string) (string, []string) {
	toks := strings.Fields(line)
	partial := ""
	if len(toks) > 0 && !strings.HasSuffix(line, " ") {
		partial = toks[len(toks)-1]
		toks = toks[:len(toks)-1]
	}
	var pool []string
	switch {
	case len(toks) == 0:
		for _, c := range tuiCommands {
			pool = append(pool, c.name)
		}
	case toks[len(toks)-1] == "-name" || toks[len(toks)-1] == "--name":
		for _, d := range m.snap.Devices {
			pool = append(pool, quoteArg(d.Name))
		}
	case toks[len(toks)-1] == "-id" || toks[len(toks)-1] == "--id":
		for _, d := range m.snap.Devices {
			pool = append(pool, d.ID)
		}
	case strings.HasPrefix(partial, "-"):
		pool = []string{"--name", "--id", "-force"}
	default:
		pos := positionals(toks)
		switch toks[0] {
		case "auth":
			if len(pos) == 0 {
				pool = []string{"login", "status", "logout"}
			}
		case "light", "sleep":
			if len(pos) == 0 {
				pool = []string{"on", "off"}
			}
		case "op":
			if dev := m.targetDevice(splitArgs(strings.Join(toks, " "))); dev != nil {
				for _, p := range dev.Props {
					if len(pos) == 0 {
						pool = append(pool, p.Code)
					} else if len(pos) == 1 && p.Code == pos[0] {
						pool = p.Values
						if p.Type == "bool" {
							pool = []string{"true", "false"}
						}
					}
				}
			}
		}
	}
	var out []string
	for _, p := range pool {
		if strings.HasPrefix(p, partial) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return partial, out
}

func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := []rune(ss[0])
	for _, s := range ss[1:] {
		r := []rune(s)
		n := 0
		for n < len(p) && n < len(r) && p[n] == r[n] {
			n++
		}
		p = p[:n]
	}
	return string(p)
}

// 候補1件で確定, 複数件は共通接頭辞まで補完
func (m *consoleUI) complete() {
	if m.cmd.pos != len(m.cmd.runes) {
		return
	}
	line := m.cmd.String()
	partial, cands := m.candidates(line)
	if len(cands) == 0 {
		return
	}
	fill := commonPrefix(cands)
	if len(cands) == 1 {
		fill += " "
	}
	m.cmd.set(strings.TrimSuffix(line, partial) + fill)
}
