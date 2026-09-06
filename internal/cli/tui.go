package cli

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"mesij/internal/project"
	"mesij/internal/store"
)

type tuiLoader func() (active []store.Event, events []store.Event, err error)

type tuiView struct {
	app      *tview.Application
	active   *tview.Table
	log      *tview.Table
	details  *detailsView
	footer   *tview.TextView
	root     *tview.Flex
	columns  *tview.Flex
	left     *tview.Flex
	loader   tuiLoader
	updating bool

	preset        tuiPreset
	showDetails   bool
	showLog       bool
	activeRows    []tuiRow
	logRows       []tuiRow
	detailsSource tuiDetailSource
	detailsID     string
	activeCursor  tableCursor
	logCursor     tableCursor
	refresh       time.Duration
}

type tableCursor struct {
	identity string
	index    int
	rowOff   int
	colOff   int
}

func newTUIView(loader tuiLoader) *tuiView {
	view := &tuiView{
		app:           tview.NewApplication(),
		loader:        loader,
		preset:        tuiPresetNormal,
		showDetails:   true,
		showLog:       true,
		detailsSource: tuiSourceActive,
		activeCursor:  tableCursor{index: 0},
		logCursor:     tableCursor{index: 0},
		refresh:       2 * time.Second,
	}
	view.active = tview.NewTable().SetFixed(1, 0).SetSelectable(true, false).SetEvaluateAllRows(true)
	view.active.SetBorder(true).SetTitle(" Active work ")
	view.log = tview.NewTable().SetFixed(1, 0).SetSelectable(true, false).SetEvaluateAllRows(true)
	view.log.SetBorder(true).SetTitle(" Event log ")
	view.details = newDetailsView()
	view.footer = tview.NewTextView().SetTextAlign(tview.AlignCenter)
	view.left = tview.NewFlex().SetDirection(tview.FlexRow)
	view.columns = tview.NewFlex().SetDirection(tview.FlexColumn)
	view.root = tview.NewFlex().SetDirection(tview.FlexRow)

	view.active.SetSelectionChangedFunc(func(row, _ int) {
		view.onTableSelect(tuiSourceActive, row)
	})
	view.log.SetSelectionChangedFunc(func(row, _ int) {
		view.onTableSelect(tuiSourceLog, row)
	})
	view.active.SetFocusFunc(func() {
		view.onTableFocus(tuiSourceActive)
	})
	view.log.SetFocusFunc(func() {
		view.onTableFocus(tuiSourceLog)
	})

	view.rebuild()
	view.app.SetRoot(view.root, true)
	view.app.SetInputCapture(view.capture)
	return view
}

func (r Runner) tui(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	refresh := fs.Duration("refresh", 2*time.Second, "refresh interval")
	limit := fs.Int("limit", 200, "messages to display")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *refresh < 100*time.Millisecond || *limit < 1 {
		fmt.Fprintln(r.Stderr, "mesij tui: invalid arguments")
		return 2
	}
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()

	view := newTUIView(func() ([]store.Event, []store.Event, error) {
		active, err := db.Active(ctx, p.ID)
		if err != nil {
			return nil, nil, err
		}
		events, err := db.List(ctx, store.Query{ProjectID: p.ID, Limit: *limit, Latest: true})
		if err != nil {
			return nil, nil, err
		}
		newestFirst := make([]store.Event, len(events))
		for i, event := range events {
			newestFirst[len(events)-1-i] = event
		}
		return active, newestFirst, nil
	})
	view.refresh = *refresh
	if err := view.load(); err != nil {
		return r.fail(err)
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(*refresh)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				view.app.QueueUpdateDraw(func() {
					_ = view.load()
				})
			case <-stop:
				return
			case <-ctx.Done():
				view.app.Stop()
				return
			}
		}
	}()
	if err := view.app.EnableMouse(true).Run(); err != nil {
		return r.fail(err)
	}
	return 0
}

func (v *tuiView) capture(ev *tcell.EventKey) *tcell.EventKey {
	switch ev.Key() {
	case tcell.KeyEsc:
		v.app.Stop()
		return nil
	case tcell.KeyTab:
		v.tab()
		return nil
	}
	switch ev.Rune() {
	case 'q':
		v.app.Stop()
		return nil
	case 'r':
		_ = v.load()
		return nil
	case '1', '2', '3':
		v.captureCursors()
		v.activeCursor.colOff = 0
		v.logCursor.colOff = 0
		v.preset = tuiPreset(ev.Rune() - '0')
		v.renderAll()
		v.setFooter(nil)
		return nil
	case 'd':
		v.toggleDetails()
		return nil
	case 'l':
		v.toggleLog()
		return nil
	}
	return ev
}

func (v *tuiView) load() error {
	active, events, err := v.loader()
	if err != nil {
		v.setFooter(err)
		return err
	}
	v.captureCursors()
	v.activeRows = make([]tuiRow, 0, len(active))
	for _, event := range active {
		v.activeRows = append(v.activeRows, newTUIRow(event, true))
	}
	v.logRows = make([]tuiRow, 0, len(events))
	for _, event := range events {
		v.logRows = append(v.logRows, newTUIRow(event, false))
	}
	v.renderAll()
	v.setFooter(nil)
	return nil
}

func (v *tuiView) renderAll() {
	v.updating = true
	v.renderTable(v.active, v.activeRows, "No active work")
	v.renderTable(v.log, v.logRows, "No messages")
	v.activeCursor = applyCursor(v.active, v.activeRows, v.activeCursor)
	v.logCursor = applyCursor(v.log, v.logRows, v.logCursor)
	v.updating = false
	v.updateDetails()
}

func (v *tuiView) renderTable(table *tview.Table, rows []tuiRow, empty string) {
	table.Clear()
	schema := tableSchema(v.preset, rows)
	setHeader(table, headerLabels(schema))
	for i, row := range rows {
		setRow(table, i+1, row.valuesFor(schema), v.preset)
	}
	if len(rows) == 0 {
		table.SetCell(1, 0, tview.NewTableCell(empty).SetSelectable(false))
	}
}

func (v *tuiView) captureCursors() {
	v.activeCursor = tableCursorFrom(v.active, v.activeRows)
	v.logCursor = tableCursorFrom(v.log, v.logRows)
}

func tableCursorFrom(table *tview.Table, rows []tuiRow) tableCursor {
	row, _ := table.GetSelection()
	index := row - 1
	identity := ""
	if index >= 0 && index < len(rows) {
		identity = rows[index].identity
	}
	rowOff, colOff := table.GetOffset()
	if index < 0 {
		index = 0
	}
	return tableCursor{identity: identity, index: index, rowOff: rowOff, colOff: colOff}
}

func applyCursor(table *tview.Table, rows []tuiRow, cursor tableCursor) tableCursor {
	if len(rows) == 0 {
		return tableCursor{index: -1}
	}
	found := -1
	if cursor.identity != "" {
		for i, row := range rows {
			if row.identity == cursor.identity {
				found = i
				break
			}
		}
	}
	if found < 0 {
		found = cursor.index
		if found < 0 {
			found = 0
		}
		if found >= len(rows) {
			found = len(rows) - 1
		}
	}
	table.Select(found+1, 0)
	table.SetOffset(cursor.rowOff, cursor.colOff)
	return tableCursor{identity: rows[found].identity, index: found, rowOff: cursor.rowOff, colOff: cursor.colOff}
}

func (v *tuiView) selected() *tuiRow {
	if v.detailsSource == tuiSourceLog {
		return rowAt(v.logRows, v.logCursor.index)
	}
	return rowAt(v.activeRows, v.activeCursor.index)
}

func rowAt(rows []tuiRow, index int) *tuiRow {
	if index < 0 || index >= len(rows) {
		return nil
	}
	return &rows[index]
}

func (v *tuiView) onTableSelect(source tuiDetailSource, row int) {
	if v.updating || row <= 0 {
		return
	}
	index := row - 1
	if source == tuiSourceActive {
		if index >= len(v.activeRows) {
			return
		}
		offRow, offCol := v.active.GetOffset()
		v.activeCursor = tableCursor{identity: v.activeRows[index].identity, index: index, rowOff: offRow, colOff: offCol}
	} else {
		if index >= len(v.logRows) {
			return
		}
		offRow, offCol := v.log.GetOffset()
		v.logCursor = tableCursor{identity: v.logRows[index].identity, index: index, rowOff: offRow, colOff: offCol}
	}
	v.detailsSource = source
	v.updateDetails()
}

func (v *tuiView) onTableFocus(source tuiDetailSource) {
	if v.updating {
		return
	}
	v.detailsSource = source
	v.syncCursorFromTable(source)
	v.updateDetails()
}

func (v *tuiView) syncCursorFromTable(source tuiDetailSource) {
	if source == tuiSourceActive {
		v.activeCursor = tableCursorFrom(v.active, v.activeRows)
		return
	}
	v.logCursor = tableCursorFrom(v.log, v.logRows)
}

func (v *tuiView) updateDetails() {
	row := v.selected()
	id := ""
	var fields []tuiField
	if row != nil {
		id = row.identity
		fields = row.details()
	}
	if v.detailsSource == tuiSourceLog {
		v.details.SetTitle(" Details · Event log ")
	} else {
		v.details.SetTitle(" Details · Active work ")
	}
	changed := id != v.detailsID || !detailsFieldsEqual(v.details.fields, fields)
	v.details.setFields(fields)
	v.detailsID = id
	if changed {
		v.details.ScrollToBeginning()
	}
}

func (v *tuiView) visiblePanes() []tview.Primitive {
	panes := []tview.Primitive{v.active}
	if v.showLog {
		panes = append(panes, v.log)
	}
	if v.showDetails {
		panes = append(panes, v.details)
	}
	return panes
}

func (v *tuiView) tab() {
	panes := v.visiblePanes()
	if len(panes) == 0 {
		return
	}
	focus := v.app.GetFocus()
	index := 0
	for i, pane := range panes {
		if pane == focus {
			index = i
			break
		}
	}
	v.app.SetFocus(panes[(index+1)%len(panes)])
}

func (v *tuiView) toggleDetails() {
	v.showDetails = !v.showDetails
	if !v.showDetails && v.app.GetFocus() == v.details {
		v.focusSourceTable()
	}
	v.rebuild()
	v.setFooter(nil)
}

func (v *tuiView) toggleLog() {
	v.showLog = !v.showLog
	if !v.showLog {
		if v.app.GetFocus() == v.log {
			v.app.SetFocus(v.active)
		}
		if v.detailsSource == tuiSourceLog {
			v.detailsSource = tuiSourceActive
			v.updateDetails()
		}
	}
	v.rebuild()
	v.setFooter(nil)
}

func (v *tuiView) focusSourceTable() {
	if v.detailsSource == tuiSourceLog && v.showLog {
		v.app.SetFocus(v.log)
		return
	}
	v.app.SetFocus(v.active)
}

func (v *tuiView) rebuild() {
	focus := v.app.GetFocus()
	v.updating = true
	v.left.Clear()
	v.left.AddItem(v.active, 0, 1, true)
	if v.showLog {
		v.left.AddItem(v.log, 0, 1, false)
	}
	v.columns.Clear()
	v.columns.AddItem(v.left, 0, 2, true)
	if v.showDetails {
		v.columns.AddItem(v.details, 0, 1, false)
	}
	v.root.Clear()
	v.root.SetDirection(tview.FlexRow)
	v.root.AddItem(v.columns, 0, 1, true)
	v.root.AddItem(v.footer, 1, 0, false)
	v.updating = false
	if focus != nil && v.paneVisible(focus) {
		v.app.SetFocus(focus)
	} else if !v.paneVisible(focus) {
		v.focusSourceTable()
	}
}

func (v *tuiView) paneVisible(pane tview.Primitive) bool {
	for _, visible := range v.visiblePanes() {
		if visible == pane {
			return true
		}
	}
	return pane == v.root || pane == v.columns || pane == v.left
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func (v *tuiView) setFooter(err error) {
	interval := v.refresh
	if interval == 0 {
		interval = 2 * time.Second
	}
	status := fmt.Sprintf("%s  details=%s  log=%s  %s  •  1/2/3 d l r Tab q",
		v.preset.name(), onOff(v.showDetails), onOff(v.showLog), interval)
	if err != nil {
		v.footer.SetText("refresh failed: " + err.Error() + "  •  " + status)
		return
	}
	v.footer.SetText(status)
}

func setHeader(table *tview.Table, values []string) {
	for column, value := range values {
		table.SetCell(0, column, tview.NewTableCell(tview.Escape(value)).
			SetTextColor(tcell.ColorYellow).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}
}

func setRow(table *tview.Table, row int, values []string, preset tuiPreset) {
	for column, value := range values {
		cell := tview.NewTableCell(tview.Escape(value))
		if preset != tuiPresetFull && column == len(values)-1 {
			cell.SetExpansion(1)
		}
		table.SetCell(row, column, cell)
	}
}
