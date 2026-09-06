package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"mesij/internal/store"
)

func TestTUIFocusTraversalAndVisibility(t *testing.T) {
	data := &scriptedStore{
		active: []store.Event{
			activeEvent("sess-a", "work-a", "first active", "plan"),
			activeEvent("sess-b", "work-b", "second active", "implement"),
		},
		events: []store.Event{
			logEvent("evt-1", "sess-a", `{"work":"work-a","message":"log one"}`),
			logEvent("evt-2", "sess-b", `{"work":"work-b","message":"log two"}`),
		},
	}
	view := newLoadedView(t, data)
	screen := attachScreen(t, view, 120, 36)

	if got := focusName(view); got != "active" {
		t.Fatalf("initial focus = %s", got)
	}
	if !strings.Contains(detailsText(view), "work-a") {
		t.Fatalf("initial details missing active row: %q", detailsText(view))
	}

	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if got := focusName(view); got != "log" {
		t.Fatalf("tab to log: focus = %s", got)
	}
	if !strings.Contains(detailsText(view), "log one") {
		t.Fatalf("focusing log should update details without moving: %q", detailsText(view))
	}

	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if got := focusName(view); got != "details" {
		t.Fatalf("tab to details: focus = %s", got)
	}
	if !strings.Contains(detailsText(view), "log one") {
		t.Fatalf("details focus should retain log source: %q", detailsText(view))
	}

	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if got := focusName(view); got != "active" {
		t.Fatalf("tab should wrap to active, not trap on log: focus = %s", got)
	}

	for _, combo := range []struct {
		name        string
		hideLog     bool
		hideDetails bool
		cycle       []string
	}{
		{name: "all", cycle: []string{"active", "log", "details", "active"}},
		{name: "no-details", hideDetails: true, cycle: []string{"active", "log", "active"}},
		{name: "no-log", hideLog: true, cycle: []string{"active", "details", "active"}},
		{name: "tables-only", hideLog: true, hideDetails: true, cycle: []string{"active", "active"}},
	} {
		t.Run(combo.name, func(t *testing.T) {
			view := newLoadedView(t, data)
			screen := attachScreen(t, view, 120, 36)
			if combo.hideDetails {
				drive(t, view, screen, runeKey('d'))
			}
			if combo.hideLog {
				drive(t, view, screen, runeKey('l'))
			}
			if got := focusName(view); got != "active" {
				t.Fatalf("start focus = %s", got)
			}
			for i, want := range combo.cycle {
				if got := focusName(view); got != want {
					t.Fatalf("step %d: focus = %s, want %s", i, got, want)
				}
				drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
			}
		})
	}

	t.Run("hide focused details", func(t *testing.T) {
		view := newLoadedView(t, data)
		screen := attachScreen(t, view, 120, 36)
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		if focusName(view) != "details" {
			t.Fatalf("expected details focus, got %s", focusName(view))
		}
		drive(t, view, screen, runeKey('d'))
		if got := focusName(view); got != "log" {
			t.Fatalf("hiding focused details should move to source table, got %s", got)
		}
	})

	t.Run("hide log preserves active selection", func(t *testing.T) {
		view := newLoadedView(t, data)
		screen := attachScreen(t, view, 120, 36)
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
		selected, _ := view.active.GetSelection()
		if selected != 2 {
			t.Fatalf("active selection before hide = %d", selected)
		}
		if !strings.Contains(detailsText(view), "work-b") {
			t.Fatalf("details should follow active row 2: %q", detailsText(view))
		}
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		if focusName(view) != "log" {
			t.Fatalf("expected log focus")
		}
		drive(t, view, screen, runeKey('l'))
		if got := focusName(view); got != "active" {
			t.Fatalf("hiding focused log should move to active, got %s", got)
		}
		got, _ := view.active.GetSelection()
		if got != selected {
			t.Fatalf("active selection reset from %d to %d", selected, got)
		}
		if !strings.Contains(detailsText(view), "work-b") {
			t.Fatalf("log hide should keep active details: %q", detailsText(view))
		}
	})
}

func TestTUISelectionIdentityAndRefresh(t *testing.T) {
	first := activeEvent("sess-a", "shared-work", "alpha", "plan")
	second := activeEvent("sess-b", "shared-work", "beta", "plan")
	third := activeEvent("sess-c", "other-work", "gamma", "plan")
	data := &scriptedStore{
		active: []store.Event{first, second, third},
		events: []store.Event{logEvent("evt-1", "sess-a", `{"work":"shared-work","message":"log"}`)},
	}
	view := newLoadedView(t, data)
	screen := attachScreen(t, view, 140, 40)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if !strings.Contains(detailsText(view), "beta") {
		t.Fatalf("selected sess-b: %q", detailsText(view))
	}
	_, colOff := view.active.GetOffset()
	drive(t, view, screen, runeKey('3'))
	for i := 0; i < 6; i++ {
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	}
	rowOff, newColOff := view.active.GetOffset()
	if newColOff <= colOff {
		t.Fatalf("expected horizontal offset to increase, got %d -> %d", colOff, newColOff)
	}

	updated := activeEvent("sess-b", "shared-work", "beta-updated", "implement")
	updated.ID = "event-sess-b-2"
	data.active = []store.Event{third, updated, first}
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	if !detailsHas(view, "beta-updated") {
		t.Fatalf("lifecycle refresh lost sess-b selection: %q", detailsText(view))
	}
	if strings.Contains(detailsText(view), "gamma") && !strings.Contains(detailsText(view), "beta-updated") {
		t.Fatalf("unfocused table rebuild stole selection")
	}
	gotRow, gotCol := view.active.GetOffset()
	if gotCol != newColOff {
		t.Fatalf("column offset %d, want %d", gotCol, newColOff)
	}
	if gotRow != rowOff {
		t.Fatalf("row offset %d, want %d", gotRow, rowOff)
	}

	data.active = []store.Event{third, first}
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	text := detailsText(view)
	if strings.Contains(text, "beta") {
		t.Fatalf("removed row still in details: %q", text)
	}
	if !strings.Contains(text, "other-work") && !strings.Contains(text, "work-a") && !strings.Contains(text, "shared-work") {
		t.Fatalf("expected nearest remaining row, got %q", text)
	}

	data.active = nil
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	if strings.TrimSpace(detailsText(view)) != "" && !strings.Contains(detailsText(view), "No ") {
		if detailsText(view) != "" && selectedDetailsIdentity(view) != "" {
			t.Fatalf("empty table should clear details, got %q", detailsText(view))
		}
	}

	data.active = []store.Event{first, second}
	data.events = []store.Event{
		logEvent("evt-long", "sess-a", `{"work":"shared-work","message":"`+strings.Repeat("line\\n", 40)+`"}`),
	}
	view = newLoadedView(t, data)
	screen = attachScreen(t, view, 120, 24)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if focusName(view) != "details" {
		t.Fatalf("want details focus")
	}
	for i := 0; i < 8; i++ {
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	}
	scrolled, _ := view.details.GetScrollOffset()
	if scrolled == 0 {
		t.Fatalf("expected details to scroll")
	}
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	kept, _ := view.details.GetScrollOffset()
	if kept != scrolled {
		t.Fatalf("unchanged details scroll %d, want %d", kept, scrolled)
	}
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if focusName(view) != "active" {
		t.Fatalf("return to active, got %s", focusName(view))
	}
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	reset, _ := view.details.GetScrollOffset()
	if reset != 0 {
		t.Fatalf("changed record should reset details scroll, got %d", reset)
	}
}

func TestTUIFullSchemaAndDetails(t *testing.T) {
	left := logEvent("evt-left", "sess", `{"work":"w1","message":"left","alpha":"A","n":9007199254740993,"data":{"nested":true},"empty":"","gone":null}`)
	right := logEvent("evt-right", "sess", `{"work":"w2","message":"right","zeta":"Z"}`)
	right.Type = "message.posted"
	data := &scriptedStore{events: []store.Event{left, right}}
	view := newLoadedView(t, data)
	screen := attachScreen(t, view, 160, 40)
	drive(t, view, screen, runeKey('3'))

	headers := rowTexts(view.log, 0)
	if idxOf(headers, "WORK") < 0 || idxOf(headers, "MESSAGE") < 0 || idxOf(headers, "PHASE") < 0 || idxOf(headers, "TASK") < 0 {
		t.Fatalf("full headers missing known fields: %v", headers)
	}
	alpha := idxOf(headers, "ALPHA")
	zeta := idxOf(headers, "ZETA")
	if alpha < 0 || zeta < 0 {
		t.Fatalf("full headers missing union keys: %v", headers)
	}
	row1 := rowTexts(view.log, 1)
	row2 := rowTexts(view.log, 2)
	if unescapeCell(row1[alpha]) != "A" || unescapeCell(row2[alpha]) != "" {
		t.Fatalf("alpha column misaligned: %v / %v", row1, row2)
	}
	if unescapeCell(row1[zeta]) != "" || unescapeCell(row2[zeta]) != "Z" {
		t.Fatalf("zeta column misaligned: %v / %v", row1, row2)
	}

	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	text := detailsText(view)
	for _, want := range []string{"work", "message", "phase", "task", "9007199254740993", `"nested": true`, "alice", "sess", "evt-left"} {
		if !detailsHas(view, want) && !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Fatalf("details missing %q in %q", want, text)
		}
	}
	if !strings.Contains(text, "(empty)") || !strings.Contains(text, "(null)") {
		t.Fatalf("details missing empty/null indication: %q", text)
	}
	lead := []string{"work", "message", "phase", "task"}
	pos := make([]int, len(lead))
	lower := strings.ToLower(text)
	for i, key := range lead {
		pos[i] = detailsKeyIndex(lower, key)
		if pos[i] < 0 {
			t.Fatalf("details missing lead field %s: %q", key, text)
		}
		if i > 0 && pos[i] < pos[i-1] {
			t.Fatalf("details order %v positions %v", lead, pos)
		}
	}

	data.events = []store.Event{logEvent("evt-bad", "sess", `{"work":`)}
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if !strings.Contains(detailsText(view), `{"work":`) {
		t.Fatalf("invalid JSON payload not visible: %q", detailsText(view))
	}

	projected := activeEvent("sess-a", "work-a", "new-msg", "implement")
	projected.Payload = json.RawMessage(`{"work":"work-a","message":"old-msg","extra":"original"}`)
	projected.Projection = json.RawMessage(`{"work":"work-a","phase":"implement","message":"new-msg"}`)
	data.active = []store.Event{projected}
	data.events = nil
	view = newLoadedView(t, data)
	_ = attachScreen(t, view, 120, 30)
	text = detailsText(view)
	if !strings.Contains(text, "old-msg") || !strings.Contains(text, "new-msg") || !strings.Contains(text, "original") {
		t.Fatalf("active details lost payload/projection: %q", text)
	}
}

func TestTUIMarkupAndWrapping(t *testing.T) {
	path := "/Users/jack/very/long/unbroken/path/" + strings.Repeat("segment/", 12) + "file.go"
	event := logEvent("evt-mark", "sess", fmt.Sprintf(`{"work":"w","message":"[literal]","path":%q,"files":["internal/cli/tui.go","README.md"]}`, path))
	view := newLoadedView(t, &scriptedStore{events: []store.Event{event}})
	screen := attachScreen(t, view, 80, 24)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	messageCol := idxOf(rowTexts(view.log, 0), "MESSAGE")
	if messageCol < 0 {
		t.Fatal("missing MESSAGE column")
	}
	cell := view.log.GetCell(1, messageCol).Text
	if cell != tview.Escape("[literal]") {
		t.Fatalf("table cell not escaped: %q", cell)
	}

	dx, dy, dw, dh := view.details.GetInnerRect()
	region := regionText(screen, dx, dy, dw, dh)
	if !strings.Contains(region, "[literal]") {
		t.Fatalf("details screen missing literal markup: %q", region)
	}
	if strings.Contains(region, "[literal[]") {
		t.Fatalf("details screen has mangled escape: %q", region)
	}
	if strings.Count(region, "segment/") < 2 && !strings.Contains(region, "file.go") {
		t.Fatalf("long path not visible: %q", region)
	}
	if !strings.Contains(region, "tui.go") || !strings.Contains(region, "README.md") {
		t.Fatalf("file list not readable: %q", region)
	}
}

func TestTUIRefreshErrorsAndPreset(t *testing.T) {
	data := &scriptedStore{
		active: []store.Event{activeEvent("sess-a", "work-a", "keep-me", "plan")},
	}
	view := newLoadedView(t, data)
	screen := attachScreen(t, view, 100, 24)
	if data.loads != 1 {
		t.Fatalf("loads = %d", data.loads)
	}
	drive(t, view, screen, runeKey('1'))
	if data.loads != 1 {
		t.Fatalf("preset switch requeryed db, loads = %d", data.loads)
	}
	headers := rowTexts(view.active, 0)
	if got := strings.Join(headers, ","); got != "WORK,MESSAGE" {
		t.Fatalf("lite headers = %q", got)
	}
	drive(t, view, screen, runeKey('2'))
	headers = rowTexts(view.active, 0)
	if got := strings.Join(headers, ","); got != "WORK,PHASE,TASK,MESSAGE" {
		t.Fatalf("normal headers = %q", got)
	}
	data.err = errors.New("db locked")
	drive(t, view, screen, runeKey('r'))
	footer := view.footer.GetText(true)
	if !strings.Contains(footer, "refresh failed") {
		t.Fatalf("footer missing refresh failure: %q", footer)
	}
	if !strings.Contains(rowTexts(view.active, 1)[0], "work-a") && unescapeCell(rowTexts(view.active, 1)[0]) != "work-a" {
		t.Fatalf("failed refresh dropped rows: %v", rowTexts(view.active, 1))
	}
}

func TestTUILayoutFooterAndRowHighlight(t *testing.T) {
	view := newLoadedView(t, &scriptedStore{
		active: []store.Event{activeEvent("sess-a", "work-a", "msg", "plan")},
		events: []store.Event{logEvent("evt-1", "sess-a", `{"work":"work-a","message":"log"}`)},
	})
	screen := attachScreen(t, view, 100, 30)
	_, _, leftW, _ := view.left.GetRect()
	_, _, detailsW, _ := view.details.GetRect()
	if leftW <= detailsW {
		t.Fatalf("left:details want 2:1, got %d:%d", leftW, detailsW)
	}
	fx, fy, fw, fh := view.footer.GetRect()
	sw, sh := screen.Size()
	if fx != 0 || fw != sw {
		t.Fatalf("footer not full width: x=%d w=%d screen=%d", fx, fw, sw)
	}
	if fy+fh != sh {
		t.Fatalf("footer y=%d h=%d screen h=%d", fy, fh, sh)
	}
	footer := view.footer.GetText(true)
	if !strings.Contains(footer, "Normal") || !strings.Contains(footer, "details=on") || !strings.Contains(footer, "log=on") {
		t.Fatalf("footer missing preset/toggle status: %q", footer)
	}
	rows, cols := view.active.GetSelectable()
	if !rows || cols {
		t.Fatalf("active selectable rows=%v cols=%v, want full row", rows, cols)
	}
	drive(t, view, screen, runeKey('d'))
	footer = view.footer.GetText(true)
	if !strings.Contains(footer, "details=off") {
		t.Fatalf("footer after d: %q", footer)
	}
	fx, fy, fw, fh = view.footer.GetRect()
	status := regionText(screen, fx, fy, fw, fh)
	if !strings.Contains(status, "Normal") || !strings.Contains(status, "details=off") {
		t.Fatalf("width-100 footer clipped status: %q", status)
	}
	drive(t, view, screen, runeKey('3'))
	drive(t, view, screen, runeKey('l'))
	if !strings.Contains(view.footer.GetText(true), "log=off") {
		t.Fatalf("footer after l: %q", view.footer.GetText(true))
	}
	drive(t, view, screen, runeKey('l'))
	if !strings.Contains(view.footer.GetText(true), "log=on") {
		t.Fatalf("footer missing log status: %q", view.footer.GetText(true))
	}
}

func TestTUIPresetResetsHorizontalOffset(t *testing.T) {
	view := newLoadedView(t, &scriptedStore{
		active: []store.Event{
			activeEvent("sess-a", "work-keep", "first", "plan"),
			activeEvent("sess-b", "work-two", "second", "plan"),
		},
	})
	screen := attachScreen(t, view, 80, 24)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	selected, _ := view.active.GetSelection()
	drive(t, view, screen, runeKey('3'))
	for i := 0; i < 12; i++ {
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	}
	if _, col := view.active.GetOffset(); col == 0 {
		t.Fatal("expected Full horizontal scroll before Lite")
	}
	drive(t, view, screen, runeKey('1'))
	if _, col := view.active.GetOffset(); col != 0 {
		t.Fatalf("Lite should reset horizontal offset, got %d", col)
	}
	got, _ := view.active.GetSelection()
	if got != selected {
		t.Fatalf("selected row %d, want %d", got, selected)
	}
	headers := rowTexts(view.active, 0)
	if strings.Join(headers, ",") != "WORK,MESSAGE" {
		t.Fatalf("lite headers = %v", headers)
	}
	ax, ay, aw, ah := view.active.GetInnerRect()
	region := regionText(screen, ax, ay, aw, ah)
	if !strings.Contains(region, "WORK") || !strings.Contains(region, "MESSAGE") || !strings.Contains(region, "work-two") {
		t.Fatalf("Lite fields not visible after Full scroll: %q", region)
	}
}

func TestTUIEmptyFocusedLogKeepsSource(t *testing.T) {
	data := &scriptedStore{
		active: []store.Event{activeEvent("sess-a", "work-a", "active-msg", "plan")},
		events: []store.Event{logEvent("evt-1", "sess-a", `{"work":"work-a","message":"log-old"}`)},
	}
	view := newLoadedView(t, data)
	screen := attachScreen(t, view, 120, 30)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if focusName(view) != "log" {
		t.Fatalf("focus = %s", focusName(view))
	}
	data.events = nil
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	if focusName(view) != "log" {
		t.Fatalf("empty log stole focus: %s", focusName(view))
	}
	if strings.Contains(detailsText(view), "active-msg") {
		t.Fatalf("empty focused log switched details to active: %q", detailsText(view))
	}
	data.events = []store.Event{logEvent("evt-2", "sess-a", `{"work":"work-a","message":"log-new"}`)}
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	view.app.ForceDraw()
	if focusName(view) != "log" {
		t.Fatalf("refill stole focus: %s", focusName(view))
	}
	text := detailsText(view)
	if !strings.Contains(text, "log-new") {
		t.Fatalf("new log rows should restore log details: %q", text)
	}
	if strings.Contains(text, "active-msg") && !strings.Contains(text, "log-new") {
		t.Fatalf("details stayed on active after log refill: %q", text)
	}
}

func TestTUIPayloadCollisionAndRawColumns(t *testing.T) {
	event := logEvent("evt-1", "sess", `{"work":"w","message":"m","actor":"payload-actor"}`)
	event.Actor = "alice"
	view := newLoadedView(t, &scriptedStore{events: []store.Event{event}})
	screen := attachScreen(t, view, 160, 30)
	drive(t, view, screen, runeKey('3'))
	headers := rowTexts(view.log, 0)
	if idxOf(headers, "PAYLOAD") < 0 {
		t.Fatalf("Full missing PAYLOAD column: %v", headers)
	}
	payloadCol := idxOf(headers, "PAYLOAD")
	if !strings.Contains(unescapeCell(rowTexts(view.log, 1)[payloadCol]), `{"work":`) {
		t.Fatalf("Full payload column missing raw JSON: %v", rowTexts(view.log, 1))
	}
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	text := detailsText(view)
	if !strings.Contains(text, "alice") {
		t.Fatalf("metadata actor lost: %q", text)
	}
	if !detailsHas(view, "payload.actor") || !detailsHas(view, "payload-actor") {
		t.Fatalf("colliding payload key not namespaced: %q", text)
	}

	bad := logEvent("evt-bad", "sess", `{"work":`)
	data := &scriptedStore{events: []store.Event{bad}}
	view = newLoadedView(t, data)
	screen = attachScreen(t, view, 160, 30)
	drive(t, view, screen, runeKey('3'))
	payloadCol = idxOf(rowTexts(view.log, 0), "PAYLOAD")
	if payloadCol < 0 || !strings.Contains(unescapeCell(rowTexts(view.log, 1)[payloadCol]), `{"work":`) {
		t.Fatalf("malformed payload not in Full column: %v", rowTexts(view.log, 1))
	}

	projected := activeEvent("sess-a", "work-a", "new-msg", "implement")
	projected.Payload = json.RawMessage(`{"work":"work-a","message":"old-msg","extra":"only-payload"}`)
	projected.Projection = json.RawMessage(`{"work":"work-a","phase":"implement","message":"new-msg"}`)
	view = newLoadedView(t, &scriptedStore{active: []store.Event{projected}})
	_ = attachScreen(t, view, 160, 30)
	drive(t, view, nil, runeKey('3'))
	headers = rowTexts(view.active, 0)
	if idxOf(headers, "PROJECTION") < 0 || idxOf(headers, "PAYLOAD") < 0 {
		t.Fatalf("active Full missing raw columns: %v", headers)
	}
}

func TestTUIHeaderAndValueEscape(t *testing.T) {
	event := logEvent("evt-1", "sess", `{"work":"w","message":"m","[red]":"[red]literal[-]"}`)
	view := newLoadedView(t, &scriptedStore{events: []store.Event{event}})
	screen := attachScreen(t, view, 140, 24)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	drive(t, view, screen, runeKey('d'))
	drive(t, view, screen, runeKey('3'))
	headers := rowTexts(view.log, 0)
	wantHeader := tview.Escape(strings.ToUpper("[red]"))
	found := idxOf(headers, wantHeader)
	if found < 0 {
		t.Fatalf("missing escaped [red] header %q in %v", wantHeader, headers)
	}
	cell := view.log.GetCell(1, found).Text
	if cell != tview.Escape("[red]literal[-]") {
		t.Fatalf("value not escaped: %q", cell)
	}
	lx, ly, lw, lh := view.log.GetInnerRect()
	for i := 0; i < found+2; i++ {
		drive(t, view, screen, tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	}
	region := regionText(screen, lx, ly, lw, lh)
	if !strings.Contains(region, "literal") {
		t.Fatalf("literal value not visible after offset %d: %q", found, region)
	}
}

func TestTUIDetailsKeyValueColumns(t *testing.T) {
	path := "/Users/jack/very/long/unbroken/path/" + strings.Repeat("segment/", 8) + "file.go"
	event := logEvent("evt-kv", "sess", fmt.Sprintf(`{"work":"w","message":"[literal]","path":%q,"files":["internal/cli/tui.go","README.md"]}`, path))
	view := newLoadedView(t, &scriptedStore{events: []store.Event{event}})
	screen := attachScreen(t, view, 100, 30)
	drive(t, view, screen, tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))

	dx, dy, dw, dh := view.details.GetInnerRect()
	workX := detailsValueX(screen, dx, dy, dw, dh, "work")
	msgX := detailsValueX(screen, dx, dy, dw, dh, "message")
	filesX := detailsValueX(screen, dx, dy, dw, dh, "files")
	if workX < 0 || msgX < 0 || filesX < 0 {
		t.Fatalf("missing value columns work=%d message=%d files=%d\n%s", workX, msgX, filesX, regionText(screen, dx, dy, dw, dh))
	}
	if workX != msgX || workX != filesX {
		t.Fatalf("value start x unaligned work=%d message=%d files=%d", workX, msgX, filesX)
	}
	if workX <= dx {
		t.Fatalf("values overlap key origin x=%d inner=%d", workX, dx)
	}

	contX, keyIntrusion := detailsContinuation(screen, dx, dy, dw, dh, workX)
	if contX < 0 {
		t.Fatalf("expected wrapped/multiline continuation\n%s", regionText(screen, dx, dy, dw, dh))
	}
	if contX != workX {
		t.Fatalf("continuation x=%d want %d", contX, workX)
	}
	if keyIntrusion {
		t.Fatalf("continuation entered key column\n%s", regionText(screen, dx, dy, dw, dh))
	}

	region := regionText(screen, dx, dy, dw, dh)
	if !strings.Contains(region, "[literal]") {
		t.Fatalf("details lost literal markup: %q", region)
	}
	if strings.Contains(region, "[literal[]") {
		t.Fatalf("details mangled escape: %q", region)
	}

	screen.SetSize(56, 30)
	view.app.ForceDraw()
	dx, dy, dw, dh = view.details.GetInnerRect()
	work2 := detailsValueX(screen, dx, dy, dw, dh, "work")
	msg2 := detailsValueX(screen, dx, dy, dw, dh, "message")
	if work2 < 0 || work2 != msg2 {
		t.Fatalf("reflow unaligned work=%d message=%d\n%s", work2, msg2, regionText(screen, dx, dy, dw, dh))
	}
	region = regionText(screen, dx, dy, dw, dh)
	if !detailsHas(view, "[literal]") || !strings.Contains(region, "w") {
		t.Fatalf("reflow dropped content: %q", region)
	}
	if strings.Contains(region, "[literal[]") {
		t.Fatalf("reflow mangled escape: %q", region)
	}
}

func TestTUIViewPresetsAndDetails(t *testing.T) {
	event := store.Event{Sequence: 4, ID: "event-1", Actor: "alice", Session: "sess", Type: "message.posted", Payload: json.RawMessage(`{"work":"w","message":"[literal]","unknown":"kept","data":{"nested":true}}`), Host: "host", User: "user", IP: "127.0.0.1"}
	row := newTUIRow(event, false)
	if got := row.valuesFor(normalFields); len(got) != 4 || got[0] != "w" || got[3] != "[literal]" {
		t.Fatalf("normal values = %#v", got)
	}
	details := row.details()
	if !containsField(details, "unknown", "kept") {
		t.Fatalf("details lost payload fields: %#v", details)
	}
	if dataValue := fieldValue(details, "data"); !strings.Contains(dataValue, "nested") {
		t.Fatalf("details lost nested data: %#v", details)
	}
	if row.identity != "event-1" {
		t.Fatalf("identity = %q", row.identity)
	}
}

func TestTUIViewActiveIdentityUsesWorkProjection(t *testing.T) {
	event := store.Event{ID: "event-1", Session: "sess", Type: "work.implementing", Payload: json.RawMessage(`{"task":"task-1","message":"old","extra":"original"}`), Projection: json.RawMessage(`{"task":"task-1","phase":"implement","message":"new"}`)}
	row := newTUIRow(event, true)
	if row.identity == "task:task-1" || !strings.Contains(row.identity, "sess") || !strings.Contains(row.identity, "task:task-1") {
		t.Fatalf("active identity should include session and work, got %q", row.identity)
	}
	if row.value("message") != "new" {
		t.Fatalf("projected message = %q", row.value("message"))
	}
	if !containsField(row.details(), "extra", "original") {
		t.Fatalf("active details lost original payload: %#v", row.details())
	}
}

type scriptedStore struct {
	active []store.Event
	events []store.Event
	err    error
	loads  int
}

func (s *scriptedStore) load() ([]store.Event, []store.Event, error) {
	s.loads++
	if s.err != nil {
		return nil, nil, s.err
	}
	return append([]store.Event(nil), s.active...), append([]store.Event(nil), s.events...), nil
}

func newLoadedView(t *testing.T, data *scriptedStore) *tuiView {
	t.Helper()
	view := newTUIView(data.load)
	if err := view.load(); err != nil {
		t.Fatal(err)
	}
	return view
}

func attachScreen(t *testing.T, view *tuiView, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { screen.Fini() })
	screen.SetSize(width, height)
	view.app.SetScreen(screen)
	view.app.SetRoot(view.root, true)
	view.app.ForceDraw()
	return screen
}

func drive(t *testing.T, view *tuiView, screen tcell.SimulationScreen, ev *tcell.EventKey) {
	t.Helper()
	if captured := view.app.GetInputCapture(); captured != nil {
		ev = captured(ev)
	}
	if ev != nil {
		if focus := view.app.GetFocus(); focus != nil {
			if handler := focus.InputHandler(); handler != nil {
				handler(ev, func(p tview.Primitive) { view.app.SetFocus(p) })
			}
		}
	}
	if screen != nil {
		view.app.ForceDraw()
	}
}

func runeKey(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func focusName(view *tuiView) string {
	switch view.app.GetFocus() {
	case view.active:
		return "active"
	case view.log:
		return "log"
	case view.details:
		return "details"
	default:
		return "other"
	}
}

func detailsText(view *tuiView) string {
	return view.details.GetText(true)
}

func detailsHas(view *tuiView, want string) bool {
	text := detailsText(view)
	if strings.Contains(text, want) {
		return true
	}
	compact := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r != ' ' && r != '\n' && r != '\t' {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return strings.Contains(compact(text), compact(want))
}

func selectedDetailsIdentity(view *tuiView) string {
	if row := view.selected(); row != nil {
		return row.identity
	}
	return ""
}

func rowTexts(table *tview.Table, row int) []string {
	out := make([]string, table.GetColumnCount())
	for col := range out {
		out[col] = table.GetCell(row, col).Text
	}
	return out
}

func idxOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func unescapeCell(text string) string {
	return tview.Unescape(text)
}

func regionText(screen tcell.SimulationScreen, x, y, width, height int) string {
	var b strings.Builder
	for row := y; row < y+height; row++ {
		for col := x; col < x+width; col++ {
			ch, _, _, _ := screen.GetContent(col, row)
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func activeEvent(session, work, message, phase string) store.Event {
	payload, _ := json.Marshal(map[string]string{"work": work, "message": message, "phase": phase})
	event := store.Event{
		ID: "event-" + session + "-" + work, ProjectID: "p", Actor: "alice", Session: session,
		Type: "work.planned", Payload: payload, Projection: payload, Worktree: "/wt", Branch: "main",
		Commit: "abc", Host: "host", User: "user", IP: "127.0.0.1", IdempotencyKey: "k-" + session,
		CreatedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
	if phase == "implement" {
		event.Type = "work.implementing"
	}
	return event
}

func logEvent(id, session, payload string) store.Event {
	return store.Event{
		ID: id, ProjectID: "p", Actor: "alice", Session: session, Type: "message.posted",
		Payload: json.RawMessage(payload), Worktree: "/wt", Branch: "main", Commit: "abc",
		Host: "host", User: "user", IP: "127.0.0.1", IdempotencyKey: "k-" + id,
		CreatedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
}

func detailsKeyIndex(text, key string) int {
	offset := 0
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimRight(line, " ")
		if strings.HasPrefix(trim, key+" ") || trim == key {
			return offset
		}
		offset += len(line) + 1
	}
	return -1
}

func detailsValueX(screen tcell.SimulationScreen, x, y, width, height int, key string) int {
	for row := y; row < y+height; row++ {
		line := strings.TrimRight(regionText(screen, x, row, width, 1), " \n")
		if !strings.HasPrefix(line, key) {
			continue
		}
		if len(line) > len(key) && line[len(key)] != ' ' && line != key {
			continue
		}
		col := 0
		for col < width {
			ch, _, _, _ := screen.GetContent(x+col, row)
			if ch == 0 {
				ch = ' '
			}
			if ch == ' ' {
				break
			}
			col++
		}
		for col < width {
			ch, _, _, _ := screen.GetContent(x+col, row)
			if ch == 0 {
				ch = ' '
			}
			if ch != ' ' {
				return x + col
			}
			col++
		}
	}
	return -1
}

func detailsContinuation(screen tcell.SimulationScreen, x, y, width, height, valueX int) (contX int, keyIntrusion bool) {
	for row := y; row < y+height; row++ {
		ch0, _, _, _ := screen.GetContent(x, row)
		if ch0 != ' ' && ch0 != 0 {
			continue
		}
		for col := 0; col < width; col++ {
			ch, _, _, _ := screen.GetContent(x+col, row)
			if ch == 0 {
				ch = ' '
			}
			if ch == ' ' {
				continue
			}
			if x+col < valueX {
				return x + col, true
			}
			return x + col, false
		}
	}
	return -1, false
}

func containsField(fields []tuiField, key, value string) bool {
	return fieldValue(fields, key) == value
}

func fieldValue(fields []tuiField, key string) string {
	for _, field := range fields {
		if field.key == key {
			return field.value
		}
	}
	return ""
}
