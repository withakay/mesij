import { type Plugin, tool } from "@opencode-ai/plugin"
import { spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { homedir } from "node:os"
import { dirname, join } from "node:path"
import { mkdir, readFile, rename, writeFile } from "node:fs/promises"

const ACTOR = "opencode"
const POLL_MS = 3000

type MesijEvent = {
  sequence: number
  id: string
  actor: string
  session: string
  type: string
  payload: Record<string, unknown>
}

function runMesij(cwd: string, args: string[], input?: unknown): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn("mesij", args, { cwd, stdio: ["pipe", "pipe", "pipe"] })
    let stdout = ""
    let stderr = ""
    child.stdout.setEncoding("utf8").on("data", (chunk) => (stdout += chunk))
    child.stderr.setEncoding("utf8").on("data", (chunk) => (stderr += chunk))
    child.on("error", reject)
    child.on("close", (code) => {
      if (code === 0) resolve(stdout)
      else reject(new Error(stderr.trim() || `mesij exited ${code}`))
    })
    if (input !== undefined) child.stdin.end(JSON.stringify(input))
    else child.stdin.end()
  })
}

function mesijSession(openCodeSession: string): string {
  return `opencode-${openCodeSession}`
}

function statePath(directory: string, session: string): string {
  const root = process.env.XDG_STATE_HOME || join(homedir(), ".local", "state")
  const key = createHash("sha256").update(`${directory}\0${session}`).digest("hex").slice(0, 32)
  return join(root, "mesij", "opencode", `${key}.cursor`)
}

async function readCursor(path: string): Promise<number> {
  try {
    const value = Number.parseInt((await readFile(path, "utf8")).trim(), 10)
    return Number.isSafeInteger(value) && value >= 0 ? value : 0
  } catch {
    return 0
  }
}

async function writeCursor(path: string, cursor: number): Promise<void> {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const temp = `${path}.${process.pid}.tmp`
  await writeFile(temp, `${cursor}\n`, { mode: 0o600 })
  await rename(temp, path)
}

function formatInbox(events: MesijEvent[]): string {
  return events
    .map((event) => {
      const message = typeof event.payload?.message === "string"
        ? event.payload.message
        : JSON.stringify(event.payload)
      return `- #${event.sequence} ${event.actor} (${event.session}): ${message}`
    })
    .join("\n")
}

function fileFromArgs(args: Record<string, unknown>): string[] {
  const files = [args.file_path, args.filePath, args.path]
    .filter((value): value is string => typeof value === "string" && value.length > 0)
  if (typeof args.patchText === "string") {
    const pattern = /^\*\*\* (?:Add File|Update File|Delete File|Move to):\s*(.+?)\s*$/gm
    for (const match of args.patchText.matchAll(pattern)) {
      if (match[1]) files.push(match[1])
    }
  }
  return [...new Set(files)]
}

export const MesijPlugin: Plugin = async (pluginContext) => {
  const { client, directory } = pluginContext as any
  const knownSessions = new Set<string>()
  const registered = new Set<string>()
  const draining = new Map<string, Promise<MesijEvent[]>>()

  async function safeToast(body: Record<string, unknown>): Promise<void> {
    try {
      await client.tui?.showToast?.({ body })
    } catch (error) {
      console.error("[mesij] toast failed:", error)
    }
  }

  async function ensureSession(openCodeID: string): Promise<string> {
    const session = mesijSession(openCodeID)
    knownSessions.add(openCodeID)
    if (!registered.has(openCodeID)) {
      await runMesij(directory, ["session", "--actor", ACTOR, "--id", session, "--json"])
      registered.add(openCodeID)
    }
    return session
  }

  async function drain(openCodeID: string, notify = true): Promise<MesijEvent[]> {
    const existing = draining.get(openCodeID)
    if (existing) return existing
    const operation = (async () => {
      const session = await ensureSession(openCodeID)
      const path = statePath(directory, session)
      const after = await readCursor(path)
      const output = await runMesij(directory, [
        "--json", "inbox", "--session", session, "--after", String(after), "--limit", "1000",
      ])
      const events = JSON.parse(output || "[]") as MesijEvent[]
      let cursor = after
      for (const event of events) cursor = Math.max(cursor, event.sequence)
      const delivered = events.filter((event) => event.session !== session)
      if (notify && delivered.length > 0) {
        const text = `New Mesij messages:\n${formatInbox(delivered)}`
        const response = await client.session.prompt({
          path: { id: openCodeID },
          body: { noReply: true, parts: [{ type: "text", text }] },
        })
        if (response?.error) throw new Error(String(response.error))
        await safeToast({ title: "Mesij", message: `${delivered.length} new message(s)`, variant: "info" })
      }
      if (cursor !== after) await writeCursor(path, cursor)
      return delivered
    })().finally(() => draining.delete(openCodeID))
    draining.set(openCodeID, operation)
    return operation
  }

  const timer = setInterval(() => {
    for (const session of knownSessions) void drain(session, true).catch((error) => {
      console.error(`[mesij] inbox drain failed for ${session}:`, error)
    })
  }, POLL_MS)
  timer.unref?.()

  return {
    dispose: async () => clearInterval(timer),

    event: async ({ event }: any) => {
      const type = event?.type
      const id = event?.properties?.info?.id || event?.properties?.sessionID || event?.properties?.sessionId
      if (typeof id !== "string") return
      if (type === "session.created" || type === "session.updated") {
        void ensureSession(id).catch((error) => console.error("[mesij] session registration failed:", error))
      }
      if (type === "session.idle") void drain(id, true).catch((error) => {
        console.error(`[mesij] inbox drain failed for ${id}:`, error)
      })
      if (type === "session.deleted") knownSessions.delete(id)
    },

    "shell.env": async (input: any, output: any) => {
      if (!input?.sessionID) return
      const session = mesijSession(input.sessionID)
      knownSessions.add(input.sessionID)
      output.env.MESIJ_ACTOR = ACTOR
      output.env.MESIJ_SESSION = session
      void ensureSession(input.sessionID).catch((error) => {
        console.error("[mesij] session registration failed:", error)
      })
    },

    "tool.execute.before": async (input: any, output: any) => {
      if (!input?.sessionID) return
      const name = String(input.tool || "").toLowerCase()
      if (!/(edit|write|patch)/.test(name)) return
      const files = fileFromArgs(output.args || {})
      if (files.length === 0) return
      let session = ""
      let external: any[] = []
      try {
        session = await ensureSession(input.sessionID)
        const args = ["--json", "check", "--session", session]
        for (const file of files) args.push("--file", file)
        const report = JSON.parse(await runMesij(directory, args))
        external = (report.active_work || []).filter((item: any) => item.session !== session)
      } catch (error) {
        console.error("[mesij] advisory pre-edit check failed:", error)
        await safeToast({ title: "Mesij warning", message: "Coordination check failed; edit was not blocked.", variant: "warning" })
        return
      }
      if (external.length > 0) {
        const warning = `Mesij reports ${external.length} overlapping external work claim(s) for ${files.join(", ")}.`
        await safeToast({ title: "Mesij overlap", message: warning, variant: "warning" })
        if (process.env.MESIJ_ENFORCE_CONFLICTS === "1") throw new Error(warning)
      }
    },

    tool: {
      mesij_inbox: tool({
        description: "Read unread Mesij messages for this OpenCode session and advance its inbox cursor.",
        args: {},
        async execute(_args, context) {
          return JSON.stringify(await drain(context.sessionID, false))
        },
      }),

      mesij_check: tool({
        description: "Check Mesij for active task, change, phase, and file conflicts.",
        args: {
          task: tool.schema.string().optional(),
          change: tool.schema.string().optional(),
          phase: tool.schema.enum(["plan", "implement"]).optional(),
          files: tool.schema.array(tool.schema.string()).optional(),
        },
        async execute(args, context) {
          const session = await ensureSession(context.sessionID)
          const command = ["--json", "check", "--session", session]
          if (args.task) command.push("--task", args.task)
          if (args.change) command.push("--change", args.change)
          if (args.phase) command.push("--phase", args.phase)
          for (const file of args.files || []) command.push("--file", file)
          return await runMesij(directory, command)
        },
      }),

      mesij_emit: tool({
        description: "Post, plan, implement, finish, defer, or reply through Mesij.",
        args: {
          event: tool.schema.enum(["plan", "implement", "start", "finish", "defer", "post", "reply"]),
          work: tool.schema.string().optional(),
          task: tool.schema.string().optional(),
          change: tool.schema.string().optional(),
          files: tool.schema.array(tool.schema.string()).optional(),
          to: tool.schema.string().optional(),
          reply_to: tool.schema.string().optional(),
          key: tool.schema.string().optional(),
          message: tool.schema.string().optional(),
          data: tool.schema.any().optional(),
        },
        async execute(args, context) {
          const session = await ensureSession(context.sessionID)
          return await runMesij(directory, ["emit"], { ...args, actor: ACTOR, session })
        },
      }),

      mesij_agents: tool({
        description: "List known Mesij actors and sessions for this project.",
        args: {},
        async execute() {
          return await runMesij(directory, ["--json", "agents"])
        },
      }),
    },
  } as any
}

export default MesijPlugin
