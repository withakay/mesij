import type { ExtensionAPI } from "@earendil-works/pi-coding-agent"
import { StringEnum } from "@earendil-works/pi-ai"
import { Type } from "typebox"
import { spawn } from "node:child_process"
import { createHash } from "node:crypto"
import { homedir } from "node:os"
import { dirname, join } from "node:path"
import { mkdir, readFile, rename, writeFile } from "node:fs/promises"

const ACTOR = "pi"
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

function cursorPath(cwd: string, session: string): string {
  const root = process.env.XDG_STATE_HOME || join(homedir(), ".local", "state")
  const key = createHash("sha256").update(`${cwd}\0${session}`).digest("hex").slice(0, 32)
  return join(root, "mesij", "pi", `${key}.cursor`)
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
  return events.map((event) => {
    const message = typeof event.payload?.message === "string"
      ? event.payload.message
      : JSON.stringify(event.payload)
    return `- #${event.sequence} ${event.actor} (${event.session}): ${message}`
  }).join("\n")
}

export default function mesijExtension(pi: ExtensionAPI) {
  let cwd = process.cwd()
  let currentSession = ""
  let timer: ReturnType<typeof setInterval> | undefined
  let draining: Promise<MesijEvent[]> | undefined
  const previousActor = process.env.MESIJ_ACTOR
  const previousSession = process.env.MESIJ_SESSION

  async function ensureSession(ctx: any): Promise<string> {
    const piID = ctx.sessionManager.getSessionId()
    if (!piID) throw new Error("Pi session has no stable session ID")
    const session = `pi-${piID}`
    cwd = ctx.cwd || cwd
    if (session !== currentSession) {
      await runMesij(cwd, ["session", "--actor", ACTOR, "--id", session, "--json"])
      currentSession = session
      process.env.MESIJ_ACTOR = ACTOR
      process.env.MESIJ_SESSION = session
    }
    return session
  }

  async function drain(ctx: any, display = true): Promise<MesijEvent[]> {
    if (draining) return draining
    draining = (async () => {
      const session = await ensureSession(ctx)
      const path = cursorPath(cwd, session)
      const after = await readCursor(path)
      const output = await runMesij(cwd, [
        "--json", "inbox", "--session", session, "--after", String(after), "--limit", "1000",
      ])
      const events = JSON.parse(output || "[]") as MesijEvent[]
      let cursor = after
      for (const event of events) cursor = Math.max(cursor, event.sequence)
      if (cursor !== after) await writeCursor(path, cursor)
      const incoming = events.filter((event) => event.session !== session)
      if (display && incoming.length > 0) {
        pi.sendMessage({
          customType: "mesij.inbox",
          content: `New Mesij messages:\n${formatInbox(incoming)}`,
          display: true,
          details: { events: incoming },
        }, { triggerTurn: false, deliverAs: "steer" })
      }
      return incoming
    })().finally(() => { draining = undefined })
    return draining
  }

  pi.on("session_start", async (_event: any, ctx: any) => {
    await ensureSession(ctx)
    await drain(ctx, true)
    if (timer) clearInterval(timer)
    timer = setInterval(() => void drain(ctx, true).catch((error) => {
      console.error("[mesij] inbox polling failed:", error)
    }), POLL_MS)
    timer.unref?.()
  })

  pi.on("before_agent_start", async (_event: any, ctx: any) => {
    await drain(ctx, true)
  })

  pi.on("session_shutdown", async () => {
    if (timer) clearInterval(timer)
    timer = undefined
    if (previousActor === undefined) delete process.env.MESIJ_ACTOR
    else process.env.MESIJ_ACTOR = previousActor
    if (previousSession === undefined) delete process.env.MESIJ_SESSION
    else process.env.MESIJ_SESSION = previousSession
  })

  pi.registerCommand("mesij-inbox", {
    description: "Read pending Mesij messages",
    handler: async (_args: string, ctx: any) => {
      const events = await drain(ctx, false)
      const text = events.length > 0 ? formatInbox(events) : "No new Mesij messages."
      ctx.ui?.notify?.(text, events.length > 0 ? "info" : "success")
    },
  } as any)

  pi.registerTool({
    name: "mesij_inbox",
    label: "Mesij inbox",
    description: "Read new Mesij messages for this Pi session.",
    parameters: Type.Object({}),
    execute: async (_id: string, _params: any, _signal: AbortSignal, _update: any, ctx: any) => {
      const events = await drain(ctx, false)
      return { content: [{ type: "text", text: JSON.stringify(events) }], details: { events } }
    },
  } as any)

  pi.registerTool({
    name: "mesij_check",
    label: "Mesij check",
    description: "Check active Mesij task, change, phase, and file conflicts.",
    parameters: Type.Object({
      task: Type.Optional(Type.String()),
      change: Type.Optional(Type.String()),
      phase: Type.Optional(StringEnum(["plan", "implement"])),
      files: Type.Optional(Type.Array(Type.String())),
    }),
    execute: async (_id: string, params: any, _signal: AbortSignal, _update: any, ctx: any) => {
      const session = await ensureSession(ctx)
      const args = ["--json", "check", "--session", session]
      if (params.task) args.push("--task", params.task)
      if (params.change) args.push("--change", params.change)
      if (params.phase) args.push("--phase", params.phase)
      for (const file of params.files || []) args.push("--file", file)
      const result = await runMesij(cwd, args)
      return { content: [{ type: "text", text: result }], details: JSON.parse(result) }
    },
  } as any)

  pi.registerTool({
    name: "mesij_emit",
    label: "Mesij emit",
    description: "Post, plan, implement, finish, defer, or reply through Mesij.",
    parameters: Type.Object({
      event: StringEnum(["plan", "implement", "start", "finish", "defer", "post", "reply"]),
      work: Type.Optional(Type.String()),
      task: Type.Optional(Type.String()),
      change: Type.Optional(Type.String()),
      files: Type.Optional(Type.Array(Type.String())),
      to: Type.Optional(Type.String()),
      reply_to: Type.Optional(Type.String()),
      key: Type.Optional(Type.String()),
      message: Type.Optional(Type.String()),
      data: Type.Optional(Type.Any()),
    }),
    execute: async (_id: string, params: any, _signal: AbortSignal, _update: any, ctx: any) => {
      const session = await ensureSession(ctx)
      const result = await runMesij(cwd, ["emit"], { ...params, actor: ACTOR, session })
      return { content: [{ type: "text", text: result }], details: JSON.parse(result) }
    },
  } as any)

  pi.registerTool({
    name: "mesij_agents",
    label: "Mesij agents",
    description: "List known Mesij actors and sessions.",
    parameters: Type.Object({}),
    execute: async (_id: string, _params: any, _signal: AbortSignal, _update: any, ctx: any) => {
      await ensureSession(ctx)
      const result = await runMesij(cwd, ["--json", "agents"])
      return { content: [{ type: "text", text: result }], details: JSON.parse(result) }
    },
  } as any)
}
