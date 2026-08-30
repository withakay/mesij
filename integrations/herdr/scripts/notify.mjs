import { createHash } from "node:crypto"
import { mkdir, readFile, rename, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import {
  eventAgentStatus,
  eventContext,
  eventPaneId,
  messageText,
  mesijSession,
  paneInfo,
  runCapture,
} from "./lib.mjs"

const event = eventContext()
const status = eventAgentStatus(event)
if (status && !new Set(["idle", "done", "blocked"]).has(status)) process.exit(0)

const paneId = eventPaneId(event)
if (!paneId) process.exit(0)

try {
  const pane = await paneInfo(paneId)
  const session = await mesijSession(pane)
  const cwd = pane?.foreground_cwd || pane?.cwd || ""
  if (!session || !cwd) process.exit(0)

  const projectResult = await runCapture("mesij", ["--json", "status"], { cwd })
  const project = JSON.parse(projectResult.stdout || "{}")
  const projectKey = project.id || `${project.database || ""}\0${project.name || cwd}`
  const path = cursorPath(projectKey, session)
  const after = await readCursor(path)
  const result = await runCapture("mesij", [
    "--json", "inbox", "--session", session, "--after", String(after), "--limit", "100",
  ], { cwd })
  const events = JSON.parse(result.stdout || "[]")
  if (!Array.isArray(events)) throw new Error("mesij inbox did not return an array")

  let cursor = after
  for (const item of events) {
    if (Number.isSafeInteger(item?.sequence)) cursor = Math.max(cursor, item.sequence)
  }
  const incoming = events.filter((item) => item?.session !== session)

  if (incoming.length > 0) {
    const body = incoming.slice(0, 3).map((item) =>
      `${item.actor || "agent"}: ${messageText(item)}`,
    ).join("\n").slice(0, 800)
    const herdr = process.env.HERDR_BIN_PATH || "herdr"
    await runCapture(herdr, [
      "notification", "show", `Mesij: ${incoming.length} new message${incoming.length === 1 ? "" : "s"}`,
      "--body", body,
      "--sound", "request",
    ])
  }

  if (cursor !== after) await writeCursor(path, cursor)
} catch (error) {
  console.error(`herdr Mesij notification failed: ${error instanceof Error ? error.message : String(error)}`)
  process.exit(0)
}

function cursorPath(projectKey, session) {
  const root = process.env.HERDR_PLUGIN_STATE_DIR || join(process.cwd(), ".state")
  const key = createHash("sha256").update(`${projectKey}\0${session}`).digest("hex").slice(0, 32)
  return join(root, "cursors", `${key}.cursor`)
}

async function readCursor(path) {
  try {
    const value = Number.parseInt((await readFile(path, "utf8")).trim(), 10)
    return Number.isSafeInteger(value) && value >= 0 ? value : 0
  } catch {
    return 0
  }
}

async function writeCursor(path, cursor) {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const temp = `${path}.${process.pid}.tmp`
  await writeFile(temp, `${cursor}\n`, { mode: 0o600 })
  await rename(temp, path)
}
