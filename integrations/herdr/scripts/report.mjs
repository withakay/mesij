import { spawn } from "node:child_process"
import { createInterface } from "node:readline/promises"

const mode = process.argv[2]
const session = process.env.MESIJ_TARGET_SESSION || ""
const projectCwd = process.env.MESIJ_PROJECT_CWD || process.cwd()

const commands = {
  tui: ["tui"],
  check: ["check", "--limit", "100"],
  agents: ["agents"],
  tail: ["tail", "--after", "0", "--follow"],
}

let args = commands[mode]
if (mode === "inbox") {
  if (!session) {
    console.error("No native agent session is available for the focused Herdr pane.")
    console.error("Install the agent's Herdr integration and Mesij harness plugin, then retry.")
    process.exit(1)
  }
  args = ["inbox", "--session", session, "--limit", "100"]
}

if (!args) {
  console.error(`unknown report mode ${JSON.stringify(mode)}`)
  process.exit(2)
}

const exitCode = await new Promise((resolve, reject) => {
  const child = spawn("mesij", args, { cwd: projectCwd, stdio: "inherit" })
  child.on("error", reject)
  child.on("close", (code) => resolve(code ?? 1))
}).catch((error) => {
  console.error(`unable to run mesij: ${error instanceof Error ? error.message : String(error)}`)
  return 1
})

if (mode === "tail" || mode === "tui") process.exit(exitCode)

console.log("\nPress Enter to close this pane.")
const input = createInterface({ input: process.stdin, output: process.stdout })
await input.question("")
input.close()
process.exit(exitCode)
