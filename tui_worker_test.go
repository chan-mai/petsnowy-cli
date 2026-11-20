package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSnapshotFountain(t *testing.T) {
	if os.Getenv("SNAP_TEST") == "" {
		t.Skip("set SNAP_TEST=1 to run (requires session)")
	}
	c := NewClient(randomHex(8))
	s, err := loadSession()
	if err != nil {
		t.Fatal("no session:", err)
	}
	if _, err := c.sessionValid(s.SID); err != nil {
		t.Fatal("session invalid:", err)
	}
	sn := c.snapshot()
	if sn.Err != "" {
		t.Fatal("snapshot err:", sn.Err)
	}
	for _, d := range sn.Devices {
		if d.ID != "ebdc5496ecc31817e1znmk" {
			continue
		}
		b, _ := json.MarshalIndent(d, "", "  ")
		t.Logf("devView:\n%s", b)
		for _, dp := range d.Status {
			t.Logf("status: %s = %s", dp.Code, dp.Value)
		}
		for _, v := range d.Values {
			_ = v
		}
		t.Logf("Values: %v", d.Values)
	}
}
