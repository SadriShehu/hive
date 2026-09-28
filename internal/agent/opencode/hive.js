// hive: reports opencode sessions to hive (github.com/SadriShehu/hive).
// Written by `hive install opencode`; `hive uninstall opencode` removes it.
import { spawn } from "node:child_process"

const HIVE = __HIVE_BIN__

// send hands one event to `hive hook opencode` without ever making opencode wait.
function send(payload) {
  try {
    const child = spawn(HIVE, ["hook", "opencode"], { stdio: ["pipe", "ignore", "ignore"], detached: true })
    child.on("error", () => {})
    child.stdin.on("error", () => {})
    child.stdin.end(JSON.stringify({ ...payload, pid: process.pid, at: Date.now() }))
    child.unref()
  } catch {}
}

const titles = new Map()
const states = new Map()

// status reports a session's state only when it changes.
function status(sessionID, state) {
  if (!sessionID || states.get(sessionID) === state) return
  states.set(sessionID, state)
  send({ event: state, session_id: sessionID })
}

export const HivePlugin = async () => ({
  event: async ({ event }) => {
    const p = event.properties ?? {}
    switch (event.type) {
      case "session.created":
      case "session.updated": {
        const info = p.info ?? {}
        if (!info.id) return
        const known = titles.has(info.id)
        if (known && titles.get(info.id) === info.title) return
        titles.set(info.id, info.title)
        send({
          event: known ? "update" : "start",
          session_id: info.id,
          parent_id: info.parentID ? "opencode:" + info.parentID : "",
          internal: !!info.parentID,
          title: info.title ?? "",
          cwd: info.directory ?? "",
        })
        return
      }
      case "session.status":
        status(p.sessionID, p.status?.type === "idle" ? "idle" : "busy")
        return
      case "session.idle":
        status(p.sessionID, "idle")
        return
      case "permission.asked":
      case "permission.updated":
        status(p.sessionID, "attention")
        return
      case "permission.replied":
        status(p.sessionID, "busy")
        return
      case "session.deleted":
        if (p.info?.id) send({ event: "end", session_id: p.info.id })
        return
    }
  },
  // Everything opencode's shell runs learns which session started it.
  "shell.env": async (input, output) => {
    if (input.sessionID) output.env.HIVE_PARENT = "opencode:" + input.sessionID
  },
})
