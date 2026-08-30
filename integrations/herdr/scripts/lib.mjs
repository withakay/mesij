import { spawn } from "node:child_process"
import { open } from "node:fs/promises"
import { basename } from "node:path"

export const PLUGIN_ID = "withakay.mesij"

export function runCapture(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd: options.cwd,
      env: options.env ?? process.env,
      stdio: ["ignore", "pipe", "pipe"],
    })
    let stdout = ""
    let stderr = ""
    child.stdout.setEncoding("utf8").on("data", (chunk) => (stdout += chunk))
    child.stderr.setEncoding("utf8").on("data", (chunk) => (stderr += chunk))
    child.on("error", reject)
    child.on("close", (code) => {
      if (code === 0 || options.allowFailure) {
        resolve({ code: code ?? 1, stdout, stderr })
      } else {
        reject(new Error(stderr.trim() || `${command} exited ${code}`))
      }
    })
  })
}

export function parseJson(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

export function findObject(value, predicate) {
  if (!value || typeof value !== "object") return null
  if (predicate(value)) return value
  for (const child of Object.values(value)) {
    const match = findObject(child, predicate)
    if (match) return match
  }
  return null
}

export function pluginContext() {
  return parseJson(process.env.HERDR_PLUGIN_CONTEXT_JSON || "{}") || {}
}

export function eventContext() {
  return parseJson(process.env.HERDR_PLUGIN_EVENT_JSON || "{}") || {}
}

export async function paneInfo(paneId) {
  if (!paneId) return null
  const herdr = process.env.HERDR_BIN_PATH || "herdr"
  const result = await runCapture(herdr, ["pane", "get", paneId])
  const parsed = parseJson(result.stdout)
  return findObject(parsed, (item) => item.pane_id === paneId) ||
    findObject(parsed, (item) => typeof item.pane_id === "string")
}

export async function invocationTarget() {
  const context = pluginContext()
  const paneId = context.focused_pane_id || process.env.HERDR_PANE_ID || ""
  const pane = await paneInfo(paneId).catch(() => null)
  const cwd = pane?.foreground_cwd || pane?.cwd || context.focused_pane_cwd ||
    context.workspace_cwd || process.cwd()
  return { context, pane, paneId, cwd, session: await mesijSession(pane) }
}

export async function mesijSession(pane) {
  const reference = pane?.agent_session
  const value = typeof reference?.value === "string" ? reference.value.trim() : ""
  if (!value) return ""
  const agent = String(reference?.agent || pane?.agent || "").toLowerCase()
  if (agent.includes("opencode")) {
    return `opencode-${value}`
  }
  if (agent === "pi" || agent.includes("pi-agent")) {
    let id = value
    if (reference?.kind === "path") {
      id = await piSessionId(value) || piSessionIdFromFilename(value)
    }
    return `pi-${id}`
  }
  return value
}

async function piSessionId(path) {
  let file
  try {
    file = await open(path, "r")
    const buffer = Buffer.alloc(64 * 1024)
    const { bytesRead } = await file.read(buffer, 0, buffer.length, 0)
    const line = buffer.subarray(0, bytesRead).toString("utf8").split(/\r?\n/, 1)[0]
    const header = JSON.parse(line)
    return header?.type === "session" && typeof header.id === "string" && header.id
      ? header.id
      : ""
  } catch {
    return ""
  } finally {
    await file?.close().catch(() => {})
  }
}

function piSessionIdFromFilename(path) {
  const file = basename(path.replaceAll("\\", "/")).replace(/\.jsonl$/i, "")
  const separator = file.indexOf("_")
  return separator >= 0 && separator < file.length - 1 ? file.slice(separator + 1) : file
}

export function eventPaneId(event) {
  const match = findObject(event, (item) => typeof item.pane_id === "string")
  return match?.pane_id || ""
}

export function eventAgentStatus(event) {
  const match = findObject(event, (item) => "agent_status" in item)
  return typeof match?.agent_status === "string" ? match.agent_status : ""
}

export function messageText(event) {
  if (typeof event?.payload?.message === "string" && event.payload.message) {
    return event.payload.message
  }
  try {
    return JSON.stringify(event.payload)
  } catch {
    return String(event.type || "message")
  }
}
