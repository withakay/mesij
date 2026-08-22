package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"mesij/internal/project"
	"mesij/internal/store"
)

// tail writes one immutable event per line. JSONL makes the stream easy for
// agents and harness plugins to consume incrementally without parsing the
// human-oriented coordination report from check.
func (r Runner) tail(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	after := fs.Int64("after", 0, "only events after this sequence")
	limit := fs.Int("limit", 100, "events per query (up to 1000)")
	follow := fs.Bool("follow", false, "continue waiting for new events")
	poll := fs.Duration("poll", time.Second, "poll interval when following")
	actor := fs.String("from", "", "filter by actor")
	typeName := fs.String("type", "", "filter by event type")
	forSession := fs.String("session", "", "include broadcasts and direct messages for this session")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	afterSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "after" {
			afterSet = true
		}
	})
	if fs.NArg() != 0 || *after < 0 || *limit < 1 || *limit > 1000 || *poll < 50*time.Millisecond {
		fmt.Fprintln(r.Stderr, "mesij tail: invalid arguments")
		return 2
	}

	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	encoder := json.NewEncoder(r.Stdout)
	cursor := *after
	latest := !afterSet

	for {
		if err := ctx.Err(); err != nil {
			return 0
		}
		through := int64(0)
		if *follow {
			through, err = db.LatestSequence(ctx, p.ID)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return 0
				}
				return r.fail(err)
			}
		}
		events, err := db.List(ctx, store.Query{
			ProjectID: p.ID, After: cursor, Through: through, Limit: *limit, Actor: *actor,
			Type: *typeName, ForSession: *forSession, Latest: latest,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return 0
			}
			return r.fail(err)
		}
		latest = false
		for _, event := range events {
			if err := encoder.Encode(event); err != nil {
				return r.fail(err)
			}
			cursor = event.Sequence
		}
		if !*follow {
			return 0
		}
		if len(events) == *limit {
			continue
		}
		if through > cursor {
			cursor = through
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(*poll):
		}
	}
}
