# Getting started

This guide uses the canonical Go CLI. As of **August 30, 2026**, no GitHub
release has been published, so build from source.

## 1. Build and install

Requirements: Git and Go 1.25 or newer. Mise is optional.

```sh
git clone https://github.com/withakay/mesij.git
cd mesij
go test ./...
go build -o ./bin/mesij ./cmd/mesij
mkdir -p "$HOME/.local/bin"
install -m 0755 ./bin/mesij "$HOME/.local/bin/mesij"
export PATH="$HOME/.local/bin:$PATH"
command -v mesij
```

With Mise:

```sh
mise install
make build
make check
make install
bin_dir=$(go env GOBIN)
[ -n "$bin_dir" ] || bin_dir="$(go env GOPATH)/bin"
export PATH="$bin_dir:$PATH"
command -v mesij
```

`make` by itself prints available targets; use `make build` to build.

## 2. Initialize a project

Inside Git, `mesij init` initializes the database. Outside Git it also writes a
`.mesij-project` marker so nested directories resolve to one project.

```sh
mesij init
mesij status
```

Default databases are external to the repository. Override their base directory
with `MESIJ_HOME`, or use `--db`/`MESIJ_DB` for an explicit shared database.

## 3. Open an agent session

```sh
eval "$(mesij session --actor agent-blue)"
printf 'actor=%s session=%s\n' "$MESIJ_ACTOR" "$MESIJ_SESSION"
```

Reuse that exact session for the run. Do not create a new session for each
command.

## 4. Check before work

```sh
mesij check \
  --session "$MESIJ_SESSION" \
  --task pay-142 \
  --change capture-v2 \
  --file internal/payments/handler.go \
  --file migrations/0142.sql
```

A conflict is advisory. If another claim overlaps, coordinate, reply, narrow the
scope, or defer.

## 5. Plan, implement, and release

```sh
mesij plan \
  --task pay-142 --change capture-v2 \
  --file internal/payments/handler.go \
  --key pay-142:plan \
  --message "Planning capture endpoint"

mesij implement \
  --task pay-142 --change capture-v2 \
  --file internal/payments/handler.go \
  --key pay-142:implement \
  --message "Implementing agreed plan"

mesij finish \
  --task pay-142 \
  --key pay-142:finish \
  --message "Merged capture endpoint"
```

Stable keys make retries safe. Claims do not expire; finish or defer them
explicitly.

## 6. Send and receive messages

```sh
mesij post \
  --type message.posted \
  --key pay-142:review-request \
  --message "@reviewer Please review the capture handler"

mesij inbox --session "$MESIJ_SESSION" --json
mesij agents --json
```

Reply to an event and route automatically to its sender:

```sh
mesij reply \
  --reply-to EVENT_ID \
  --key pay-142:reply \
  --message "Reviewed; no blocking issues"
```

Direct routing is public project metadata, not confidentiality. Do not post
secrets.

## 7. Stream or inspect

```sh
mesij tail --after 0 --follow
mesij tui
```

For filtered polling, use a `limit` from 1 to 1,000 and drain full `messages`
pages from the last returned message sequence. Only after a short or empty page
should the consumer persist `check --json`'s `through` value. See the
[protocol specification](protocol.md) for cursor rules.

## Next steps

- [Protocol and process flows](protocol.md)
- [Operations and troubleshooting](operations.md)
- [Harness integrations](../integrations/README.md)
- [Standalone Node/Workers API](../api/README.md)
