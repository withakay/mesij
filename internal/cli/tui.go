package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"mesij/internal/project"
	"mesij/internal/store"
)

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

	app := tview.NewApplication()
	activeTable := tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	activeTable.SetBorder(true).SetTitle(" Active work ")
	messageTable := tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	messageTable.SetBorder(true).SetTitle(" Event log ")
	footer := tview.NewTextView().SetTextAlign(tview.AlignCenter)
	footer.SetText("q/Esc quit  •  r refresh  •  Tab switch pane  •  auto-refresh " + refresh.String())

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(activeTable, 0, 2, true).
		AddItem(messageTable, 0, 3, false).
		AddItem(footer, 1, 0, false)

	load := func() error {
		active, err := db.Active(ctx, p.ID)
		if err != nil {
			return err
		}
		events, err := db.List(ctx, store.Query{ProjectID: p.ID, Limit: *limit, Latest: true})
		if err != nil {
			return err
		}
		populateActiveTable(activeTable, active)
		populateMessageTable(messageTable, events)
		return nil
	}
	if err := load(); err != nil {
		return r.fail(err)
	}

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			app.Stop()
			return nil
		case tcell.KeyTab:
			if app.GetFocus() == activeTable {
				app.SetFocus(messageTable)
			} else {
				app.SetFocus(activeTable)
			}
			return nil
		}
		switch event.Rune() {
		case 'q':
			app.Stop()
			return nil
		case 'r':
			if err := load(); err != nil {
				footer.SetText("refresh failed: " + err.Error())
			}
			return nil
		}
		return event
	})

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(*refresh)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				app.QueueUpdateDraw(func() {
					if err := load(); err != nil {
						footer.SetText("refresh failed: " + err.Error())
					}
				})
			case <-stop:
				return
			case <-ctx.Done():
				app.Stop()
				return
			}
		}
	}()

	if err := app.SetRoot(flex, true).EnableMouse(true).Run(); err != nil {
		return r.fail(err)
	}
	return 0
}

func populateActiveTable(table *tview.Table, events []store.Event) {
	table.Clear()
	setHeader(table, []string{"SEQ", "ACTOR", "SESSION", "TASK", "BRANCH", "FILES", "MESSAGE"})
	for row, event := range events {
		var payload messagePayload
		_ = json.Unmarshal(event.Payload, &payload)
		values := []string{
			fmt.Sprint(event.Sequence), event.Actor, event.Session, payload.Task, event.Branch,
			strings.Join(payload.Files, ", "), payload.Message,
		}
		setRow(table, row+1, values)
	}
	if len(events) == 0 {
		table.SetCellSimple(1, 0, "No active work")
	}
}

func populateMessageTable(table *tview.Table, events []store.Event) {
	table.Clear()
	setHeader(table, []string{"SEQ", "TIME", "ACTOR", "SESSION", "TO", "TYPE", "TASK", "FILES", "MESSAGE"})
	// The newest events are most useful to humans, so display them first.
	for row, i := 0, len(events)-1; i >= 0; row, i = row+1, i-1 {
		event := events[i]
		var payload messagePayload
		_ = json.Unmarshal(event.Payload, &payload)
		values := []string{
			fmt.Sprint(event.Sequence), event.CreatedAt.Local().Format("15:04:05"), event.Actor, event.Session,
			event.Recipient, event.Type, payload.Task, strings.Join(payload.Files, ", "), payload.Message,
		}
		setRow(table, row+1, values)
	}
	if len(events) == 0 {
		table.SetCellSimple(1, 0, "No messages")
	}
}

func setHeader(table *tview.Table, values []string) {
	for column, value := range values {
		table.SetCell(0, column, tview.NewTableCell(value).
			SetTextColor(tcell.ColorYellow).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false))
	}
}

func setRow(table *tview.Table, row int, values []string) {
	for column, value := range values {
		cell := tview.NewTableCell(value).SetExpansion(1)
		if column == 0 {
			cell.SetTextColor(tcell.ColorAqua).SetExpansion(0)
		}
		table.SetCell(row, column, cell)
	}
}
