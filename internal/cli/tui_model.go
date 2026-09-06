package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"mesij/internal/store"
)

type tuiPreset int

const (
	tuiPresetLite tuiPreset = iota + 1
	tuiPresetNormal
	tuiPresetFull
)

func (p tuiPreset) name() string {
	switch p {
	case tuiPresetLite:
		return "Lite"
	case tuiPresetFull:
		return "Full"
	default:
		return "Normal"
	}
}

type tuiField struct{ key, value string }

type tuiRow struct {
	identity string
	cells    map[string]string
	fields   []tuiField
}

type tuiDetailSource int

const (
	tuiSourceActive tuiDetailSource = iota
	tuiSourceLog
)

var knownTableFields = []string{
	"sequence", "id", "created_at", "actor", "session", "recipient", "reply_to", "type",
	"work", "message", "phase", "task", "change", "files", "data", "mentions",
	"worktree", "branch", "commit", "host", "user", "ip", "project", "idempotency_key",
	"payload", "projection",
}

var liteFields = []string{"work", "message"}
var normalFields = []string{"work", "phase", "task", "message"}

var detailsPayload = []string{"change", "files", "data", "mentions"}
var detailsMeta = []string{
	"sequence", "id", "project", "actor", "session", "recipient", "reply_to", "type",
	"worktree", "branch", "commit", "host", "user", "ip", "idempotency_key", "created_at",
}

func decodeJSONMap(raw json.RawMessage) (map[string]any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return object, true
}

func formatJSONValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "(null)"
	case string:
		if typed == "" {
			return "(empty)"
		}
		return typed
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func formatDetailsValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "(null)"
	case string:
		if typed == "" {
			return "(empty)"
		}
		return typed
	case json.Number:
		return typed.String()
	case map[string]any, []any:
		encoded, err := json.MarshalIndent(typed, "", "  ")
		if err != nil {
			return formatJSONValue(typed)
		}
		return string(encoded)
	default:
		return formatJSONValue(value)
	}
}

func displayJSON(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "(empty)"
	}
	if !json.Valid(raw) {
		return string(raw)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

func metadataCollision(key string) bool {
	switch key {
	case "sequence", "id", "created_at", "actor", "session", "recipient", "reply_to", "type",
		"worktree", "branch", "commit", "host", "user", "ip", "project", "idempotency_key",
		"payload", "projection":
		return true
	default:
		return false
	}
}

func newTUIRow(event store.Event, active bool) tuiRow {
	payloadMap, payloadOK := decodeJSONMap(event.Payload)
	projectionMap, projectionOK := decodeJSONMap(event.Projection)

	var display messagePayload
	if active {
		display = activePayload(event)
	} else if payloadOK {
		_ = json.Unmarshal(event.Payload, &display)
	}
	phase := lifecycleDisplayPhase(event, display)

	identity := event.ID
	if active {
		identity = event.Session + "\x1f" + display.workID()
	}

	cells := map[string]string{
		"sequence":        fmt.Sprint(event.Sequence),
		"id":              event.ID,
		"created_at":      event.CreatedAt.Format(time.RFC3339Nano),
		"actor":           event.Actor,
		"session":         event.Session,
		"recipient":       event.Recipient,
		"reply_to":        event.ReplyTo,
		"type":            event.Type,
		"work":            display.Work,
		"message":         display.Message,
		"phase":           phase,
		"task":            display.Task,
		"change":          display.Change,
		"files":           strings.Join(display.Files, ", "),
		"mentions":        strings.Join(display.Mentions, ", "),
		"worktree":        event.Worktree,
		"branch":          event.Branch,
		"commit":          event.Commit,
		"host":            event.Host,
		"user":            event.User,
		"ip":              event.IP,
		"project":         event.ProjectID,
		"idempotency_key": event.IdempotencyKey,
	}
	if display.Work == "" && display.workID() != "unknown" {
		cells["work"] = display.workID()
	}
	if len(display.Data) > 0 {
		cells["data"] = string(display.Data)
	}
	cells["payload"] = string(event.Payload)
	if len(bytes.TrimSpace(event.Projection)) > 0 {
		cells["projection"] = string(event.Projection)
	}

	sourceMap := payloadMap
	if active && projectionOK {
		sourceMap = projectionMap
	}
	if payloadMap != nil {
		for key, value := range payloadMap {
			if metadataCollision(key) {
				cells["payload."+key] = formatJSONValue(value)
				continue
			}
			if cells[key] == "" {
				cells[key] = formatJSONValue(value)
			}
		}
	}
	if sourceMap != nil {
		for key, value := range sourceMap {
			if metadataCollision(key) {
				continue
			}
			if cells[key] == "" {
				cells[key] = formatJSONValue(value)
			}
		}
	}

	fields := make([]tuiField, 0, 32)
	seen := map[string]bool{}
	addField := func(key, value string) {
		if seen[key] {
			return
		}
		seen[key] = true
		fields = append(fields, tuiField{key, value})
	}

	addField("work", detailOrEmpty(cells["work"]))
	addField("message", detailOrEmpty(cells["message"]))
	addField("phase", detailOrEmpty(phase))
	addField("task", detailOrEmpty(cells["task"]))
	for _, key := range detailsPayload {
		if key == "files" && len(display.Files) > 0 {
			addField(key, "\n"+strings.Join(display.Files, "\n"))
			continue
		}
		if key == "data" {
			if len(display.Data) > 0 && json.Valid(display.Data) {
				addField(key, displayJSON(display.Data))
			} else if value, ok := sourceValue(sourceMap, "data"); ok {
				addField(key, formatDetailsValue(value))
			} else if cells["data"] != "" {
				addField(key, cells["data"])
			}
			continue
		}
		if cells[key] != "" {
			addField(key, cells[key])
		}
	}

	unknown := map[string]any{}
	if payloadMap != nil {
		for key, value := range payloadMap {
			unknown[key] = value
		}
	}
	if active && projectionMap != nil {
		for key, value := range projectionMap {
			unknown[key] = value
		}
	}
	extraKeys := make([]string, 0)
	for key := range unknown {
		if key == "payload" || key == "projection" {
			continue
		}
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		label := key
		if metadataCollision(key) {
			label = "payload." + key
		}
		if seen[label] {
			continue
		}
		addField(label, formatDetailsValue(unknown[key]))
	}
	for _, key := range detailsMeta {
		addField(key, detailOrEmpty(cells[key]))
	}

	if !payloadOK && len(bytes.TrimSpace(event.Payload)) > 0 {
		addField("payload", string(event.Payload))
	} else {
		addField("payload", displayJSON(event.Payload))
	}
	if active && len(bytes.TrimSpace(event.Projection)) > 0 {
		addField("projection", displayJSON(event.Projection))
	}

	return tuiRow{identity: identity, cells: cells, fields: fields}
}

func sourceValue(object map[string]any, key string) (any, bool) {
	if object == nil {
		return nil, false
	}
	value, ok := object[key]
	return value, ok
}

func detailOrEmpty(value string) string {
	if value == "" {
		return "(empty)"
	}
	return value
}

func (r tuiRow) value(key string) string {
	if r.cells != nil {
		if value, ok := r.cells[key]; ok {
			return value
		}
	}
	for _, field := range r.fields {
		if field.key == key {
			return field.value
		}
	}
	return ""
}

func (r tuiRow) valuesFor(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = r.value(key)
	}
	return out
}

func (r tuiRow) details() []tuiField {
	return r.fields
}

func tableSchema(preset tuiPreset, rows []tuiRow) []string {
	switch preset {
	case tuiPresetLite:
		return append([]string{}, liteFields...)
	case tuiPresetNormal:
		return append([]string{}, normalFields...)
	}
	seen := make(map[string]bool, len(knownTableFields))
	for _, key := range knownTableFields {
		seen[key] = true
	}
	extra := make([]string, 0)
	for _, row := range rows {
		for key := range row.cells {
			if seen[key] {
				continue
			}
			seen[key] = true
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return append(append([]string{}, knownTableFields...), extra...)
}

func headerLabels(keys []string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = strings.ToUpper(key)
	}
	return out
}
