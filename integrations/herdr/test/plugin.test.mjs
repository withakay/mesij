import assert from "node:assert/strict"
import test from "node:test"
import { mkdtemp, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { eventAgentStatus, eventPaneId, findObject, mesijSession } from "../scripts/lib.mjs"

test("maps Herdr native sessions to Mesij harness sessions", async () => {
  assert.equal(await mesijSession({ agent: "claude", agent_session: { agent: "claude", value: "abc" } }), "abc")
  assert.equal(await mesijSession({ agent: "opencode", agent_session: { agent: "opencode", value: "abc" } }), "opencode-abc")
  assert.equal(await mesijSession({ agent: "opencode", agent_session: { agent: "opencode", value: "opencode-abc" } }), "opencode-opencode-abc")
  assert.equal(await mesijSession({ agent: "pi", agent_session: { agent: "pi", value: "abc" } }), "pi-abc")

  const directory = await mkdtemp(join(tmpdir(), "herdr-mesij-pi-"))
  const sessionFile = join(directory, "custom.jsonl")
  await writeFile(sessionFile, `${JSON.stringify({ type: "session", id: "native-pi-id" })}\n`)
  assert.equal(await mesijSession({
    agent: "pi",
    agent_session: { agent: "pi", kind: "path", value: sessionFile },
  }), "pi-native-pi-id")
  assert.equal(await mesijSession({ agent_session: null }), "")
})

test("finds pane and status fields in Herdr event envelopes", () => {
  const event = {
    event: "pane.agent_status_changed",
    data: { pane_id: "w1:p2", agent_status: "blocked" },
  }
  assert.equal(eventPaneId(event), "w1:p2")
  assert.equal(eventAgentStatus(event), "blocked")
  assert.deepEqual(findObject(event, (item) => item.pane_id === "w1:p2"), event.data)
})
