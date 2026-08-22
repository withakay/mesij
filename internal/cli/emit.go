package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"mesij/internal/project"
)

type emitInput struct {
	Event   string          `json:"event,omitempty"`
	Type    string          `json:"type,omitempty"`
	Actor   string          `json:"actor,omitempty"`
	Session string          `json:"session,omitempty"`
	To      string          `json:"to,omitempty"`
	ReplyTo string          `json:"reply_to,omitempty"`
	Key     string          `json:"key,omitempty"`
	Work    string          `json:"work,omitempty"`
	Task    string          `json:"task,omitempty"`
	Change  string          `json:"change,omitempty"`
	Phase   string          `json:"phase,omitempty"`
	Message string          `json:"message,omitempty"`
	Files   []string        `json:"files,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (r Runner) emit(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("emit", flag.ContinueOnError)
	var flagErrors bytes.Buffer
	fs.SetOutput(&flagErrors)
	inputPath := fs.String("input", "-", "JSON input path, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return r.emitFailure(2, strings.TrimSpace(flagErrors.String()))
	}
	if fs.NArg() != 0 {
		return r.emitFailure(2, "mesij emit: unexpected arguments")
	}

	reader := r.Stdin
	var file *os.File
	if *inputPath != "-" {
		var err error
		file, err = os.Open(*inputPath)
		if err != nil {
			return r.emitFailure(1, fmt.Sprintf("open JSON input: %v", err))
		}
		defer file.Close()
		reader = file
	}
	if reader == nil {
		reader = os.Stdin
	}

	const maxInput = 4 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxInput+1))
	if err != nil {
		return r.emitFailure(2, fmt.Sprintf("read JSON input: %v", err))
	}
	if len(body) > maxInput {
		return r.emitFailure(2, "JSON input exceeds 4 MiB")
	}
	fields, err := validateEmitObject(body)
	if err != nil {
		return r.emitFailure(2, fmt.Sprintf("decode JSON input: %v", err))
	}
	for name, raw := range fields {
		if name != "data" && string(bytes.TrimSpace(raw)) == "null" {
			return r.emitFailure(2, fmt.Sprintf("JSON field %q cannot be null", name))
		}
	}

	var input emitInput
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return r.emitFailure(2, fmt.Sprintf("decode JSON input: %v", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return r.emitFailure(2, "JSON input must contain exactly one object")
		}
		return r.emitFailure(2, fmt.Sprintf("decode trailing JSON: %v", err))
	}
	for _, file := range input.Files {
		if strings.TrimSpace(file) == "" {
			return r.emitFailure(2, "files must contain non-empty strings")
		}
	}

	eventType, err := input.eventType()
	if err != nil {
		return r.emitFailure(2, err.Error())
	}
	commandArgs := input.commonArgs()
	var code int
	var output, commandErrors bytes.Buffer
	child := r
	child.Stdout = &output
	child.Stderr = &commandErrors

	if isLifecycleType(eventType) {
		if len(input.Data) > 0 || input.To != "" || input.ReplyTo != "" || input.Phase != "" && input.Phase != lifecyclePhase(eventType) {
			return r.emitFailure(2, "lifecycle JSON contains incompatible data, routing, or phase fields")
		}
		code = child.lifecycle(ctx, p, append(commandArgs, "--json"), eventType)
	} else {
		commandArgs = append(commandArgs, "--type", eventType)
		if input.To != "" {
			commandArgs = append(commandArgs, "--to", input.To)
		}
		if input.ReplyTo != "" {
			commandArgs = append(commandArgs, "--reply-to", input.ReplyTo)
		}
		if input.Phase != "" {
			commandArgs = append(commandArgs, "--phase", input.Phase)
		}
		if len(input.Data) > 0 {
			commandArgs = append(commandArgs, "--data", string(input.Data))
		}
		code = child.post(ctx, p, append(commandArgs, "--json"), eventType, eventType == "message.replied")
	}
	if code != 0 {
		message := strings.TrimSpace(commandErrors.String())
		if message == "" {
			message = "emit command failed"
		}
		return r.emitFailure(code, message)
	}
	if _, err := io.Copy(r.Stdout, &output); err != nil {
		return r.emitFailure(1, err.Error())
	}
	return 0
}

func validateEmitObject(body []byte) (map[string]json.RawMessage, error) {
	allowed := map[string]bool{
		"event": true, "type": true, "actor": true, "session": true,
		"to": true, "reply_to": true, "key": true, "work": true,
		"task": true, "change": true, "phase": true, "message": true,
		"files": true, "data": true,
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	opening, ok := token.(json.Delim)
	if !ok || opening != '{' {
		return nil, errors.New("top-level JSON value must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("JSON object key must be a string")
		}
		if !allowed[name] {
			return nil, fmt.Errorf("unknown field %q", name)
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("JSON input must contain exactly one object")
		}
		return nil, err
	}
	return fields, nil
}

func (in emitInput) eventType() (string, error) {
	if in.Type != "" && in.Event != "" {
		return "", errors.New("use either type or event, not both")
	}
	if in.Type != "" {
		return in.Type, nil
	}
	switch in.Event {
	case "plan":
		return "work.planned", nil
	case "implement":
		return "work.implementing", nil
	case "start":
		return "work.started", nil
	case "finish":
		return "work.finished", nil
	case "defer":
		return "work.deferred", nil
	case "reply":
		return "message.replied", nil
	case "post":
		return "message.posted", nil
	case "":
		return "", errors.New("JSON input requires type or event")
	default:
		return "", fmt.Errorf("unknown lifecycle event %q", in.Event)
	}
}

func (in emitInput) commonArgs() []string {
	args := make([]string, 0, 16+len(in.Files)*2)
	for _, item := range []struct{ name, value string }{
		{"--actor", in.Actor}, {"--session", in.Session}, {"--key", in.Key},
		{"--work", in.Work}, {"--task", in.Task}, {"--change", in.Change},
		{"--message", in.Message},
	} {
		if item.value != "" {
			args = append(args, item.name, item.value)
		}
	}
	for _, file := range in.Files {
		args = append(args, "--file", file)
	}
	return args
}

func (r Runner) emitFailure(code int, message string) int {
	if message == "" {
		message = "invalid JSON request"
	}
	_ = json.NewEncoder(r.Stdout).Encode(struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		ExitCode int    `json:"exit_code"`
	}{false, message, code})
	return code
}
