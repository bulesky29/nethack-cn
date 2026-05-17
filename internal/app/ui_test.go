package app

import (
	"fmt"
	"strings"
	"testing"
)

// TestStatusRender exists purely as a printable smoke test for the
// status pane layout. Run with `go test -run TestStatusRender -v` and
// eyeball the colour/box output to verify alignment before committing.
//
// We test against a representative two-row status string captured from
// the debug log of a live session.
func TestStatusRender(t *testing.T) {
	raw := "[Bulesky29 the Troglodyte      ] St:18/03 Dx:13 Co:17 In:7 Wi:10 Ch:7 Lawful\n" +
		"Dlvl:1 $:0 HP:7(23) Pw:5(5) AC:8 Xp:2 T:129 Hungry Confused"

	f := parseStatus(raw, nil)

	if f.name != "Bulesky29" || f.title != "Troglodyte" || f.align != "Lawful" {
		t.Errorf("parse header wrong: %+v", f)
	}
	if f.hp != 7 || f.hpMax != 23 {
		t.Errorf("parse HP wrong: %d/%d", f.hp, f.hpMax)
	}
	if f.pw != 5 || f.pwMax != 5 {
		t.Errorf("parse Pw wrong: %d/%d", f.pw, f.pwMax)
	}
	if len(f.conditions) != 2 {
		t.Errorf("expected 2 conditions, got %v", f.conditions)
	}

	frame := renderStatusFrame(f, 90)
	fmt.Println("--- rendered status frame (90 cols) ---")
	for _, line := range frame.lines {
		fmt.Println(line)
	}
	fmt.Println("--- end ---")

	// Verify each rendered row's visible width is exactly the pane width.
	want := paneWidth(90)
	for i, line := range frame.lines {
		got := visibleWidth(line)
		if got != want {
			t.Errorf("row %d: visible width %d, want %d (line=%q)",
				i, got, want, stripANSI(line))
		}
	}
}

// TestStatusRenderSmall checks that an empty / minimal status doesn't
// blow up and still produces a valid four-row frame.
func TestStatusRenderSmall(t *testing.T) {
	f := parseStatus("Dlvl:1 HP:1(1)", nil)
	frame := renderStatusFrame(f, 80)
	if len(frame.lines) != 4 {
		t.Fatalf("want 4 lines, got %d", len(frame.lines))
	}
	for _, line := range frame.lines {
		if strings.Contains(stripANSI(line), "\x1b") {
			t.Errorf("stripANSI left an escape behind: %q", stripANSI(line))
		}
	}
}
