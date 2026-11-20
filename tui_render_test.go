package main

import (
	"strings"
	"testing"

	tui "github.com/grindlemire/go-tui"
)

func TestRenderFountainStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	c := NewClient(randomHex(8))
	s, err := loadSession()
	if err != nil {
		t.Skip("no session")
	}
	if _, err := c.sessionValid(s.SID); err != nil {
		t.Skip("session invalid")
	}
	sn := c.snapshot()
	m := newConsoleUI(c)
	m.applySnapshot(sn)
	m.devRows, m.ctrlRows = map[int]*tui.Element{}, map[int]*tui.Element{}

	d := m.selected()
	if d == nil || d.ID != "ebdc5496ecc31817e1znmk" {
		// 給水器を選択
		found := false
		for _, dv := range sn.Devices {
			if dv.ID == "ebdc5496ecc31817e1znmk" {
				m.selectDevice(dv.ID)
				found = true
			}
		}
		if !found {
			t.Fatal("fountain not in snapshot")
		}
	}

	side := m.renderSide(44)
	// 実アプリ相当のレイアウト階層で検証
	body := row(tui.WithHeight(0), tui.WithFlexGrow(1))
	body.AddChild(side)
	root := column(tui.WithSize(120, 40))
	root.AddChild(body)
	tui.Calculate(root, 120, 40)
	_out := tui.Sprint(root, tui.WithPrintWidth(120))
	if !strings.Contains(_out, "filter_days") {
		t.Errorf("full-layout render missing filter_days")
	} else {
		t.Logf("full-layout contains filter_days")
	}
	var walk func(e *tui.Element, d int)
	all := ""
	walk = func(e *tui.Element, d int) {
		for _, ch := range e.Children() {
			txt := ch.Text()
			if len(txt) > 0 {
				all += txt + "\n"
			}
			walk(ch, d+1)
		}
	}
	walk(side, 0)
	out := all
	for _, want := range []string{"filter_days", "17"} {
		if !strings.Contains(out, want) {
			t.Errorf("side render missing %q", want)
		}
	}
	t.Logf("SIDE: %q", out)

	// Controls: filter_life / time_clean (rw)
	ctrl := m.renderControls(60, 10)
	var walk2 func(e *tui.Element)
	all2 := ""
	walk2 = func(e *tui.Element) {
		for _, ch := range e.Children() {
			for _, sp := range ch.RichText() {
				all2 += sp.Text + "\n"
			}
			if t := ch.Text(); len(t) > 0 {
				all2 += t + "\n"
			}
			walk2(ch)
		}
	}
	walk2(ctrl)
	out2 := all2
	for _, want := range []string{"filter_life", "time_clean"} {
		if !strings.Contains(out2, want) {
			t.Errorf("controls render missing %q", want)
		}
	}
}
