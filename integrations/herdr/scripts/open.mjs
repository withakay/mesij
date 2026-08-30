import { invocationTarget, PLUGIN_ID, runCapture } from "./lib.mjs"

const entrypoint = process.argv[2]
const allowed = new Set(["tui", "check", "inbox", "agents", "tail"])
if (!allowed.has(entrypoint)) {
  console.error(`unknown Mesij pane ${JSON.stringify(entrypoint)}`)
  process.exit(2)
}

try {
  const target = await invocationTarget()
  const herdr = process.env.HERDR_BIN_PATH || "herdr"
  const args = [
    "plugin", "pane", "open",
    "--plugin", PLUGIN_ID,
    "--entrypoint", entrypoint,
    "--focus",
  ]
  args.push("--env", `MESIJ_PROJECT_CWD=${target.cwd}`)
  if (target.session) args.push("--env", `MESIJ_TARGET_SESSION=${target.session}`)
  const result = await runCapture(herdr, args)
  process.stdout.write(result.stdout)
  process.stderr.write(result.stderr)
} catch (error) {
  console.error(`herdr Mesij action failed: ${error instanceof Error ? error.message : String(error)}`)
  process.exit(1)
}
