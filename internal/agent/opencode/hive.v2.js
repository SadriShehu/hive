// hive: reports opencode sessions to hive (github.com/SadriShehu/hive).
// Written by `hive install opencode`; `hive uninstall opencode` removes it.
// OpenCode 2 plugin shape: a default export with an id and a setup function.
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
const dirs = new Map()
const states = new Map()

// status reports a session's state only when it changes.
function status(sessionID, state) {
  if (!sessionID || states.get(sessionID) === state) return
  states.set(sessionID, state)
  send({ event: state, session_id: sessionID })
}

// start reports a session once its id is known and updates it when hive
// learns more, so the first sighting is a start and the rest are updates.
function start(sessionID, { parentID, internal, title, cwd }) {
  if (!sessionID) return
  const known = titles.has(sessionID)
  if (known && titles.get(sessionID) === (title ?? "") && (dirs.get(sessionID) ?? "") === (cwd ?? "")) return
  titles.set(sessionID, title ?? "")
  if (cwd) dirs.set(sessionID, cwd)
  send({
    event: known ? "update" : "start",
    session_id: sessionID,
    parent_id: parentID ? "opencode:" + parentID : "",
    internal: !!internal,
    title: title ?? "",
    cwd: cwd ?? "",
  })
}

export default {
  id: "hive",
  async setup(ctx) {
    const controller = new AbortController()

    // OpenCode 2's shell hook names no session, so hive attributes a created
    // shell to the session whose bash tool call most recently started. Best
    // effort: concurrent bash calls from several sessions may misattribute,
    // and shells outside a bash call get no HIVE_PARENT.
    let lastBash = ""
    await ctx.tool.hook("execute.before", (event) => {
      if (event.tool === "bash") lastBash = event.sessionID
    })
    await ctx.shell.hook("create.before", (event) => {
      if (lastBash) event.env.HIVE_PARENT = "opencode:" + lastBash
    })

    void (async () => {
      for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
        const p = event.data ?? {}
        switch (event.type) {
          case "session.created": {
            // A new session; subagents fork instead and arrive via session.forked.
            start(p.sessionID, {
              parentID: p.parentID,
              internal: !!p.parentID,
              title: p.title ?? "",
              cwd: p.location?.directory ?? "",
            })
            return
          }
          case "session.forked": {
            // A subagent session: forked sessions emit no session.created.
            // They inherit the parent's directory, which hive recorded earlier.
            start(p.sessionID, {
              parentID: p.parentID,
              internal: true,
              title: "",
              cwd: dirs.get(p.parentID) ?? "",
            })
            return
          }
          case "session.renamed":
            start(p.sessionID, { title: p.title })
            return
          case "session.execution.started":
            status(p.sessionID, "busy")
            return
          case "session.execution.succeeded":
          case "session.execution.failed":
          case "session.execution.interrupted":
            // Interrupted covers aborted turns, which emit no other end event.
            status(p.sessionID, "idle")
            return
          case "permission.asked":
            status(p.sessionID, "attention")
            return
          case "permission.replied":
            status(p.sessionID, "busy")
            return
          case "session.deleted":
            if (p.sessionID) send({ event: "end", session_id: p.sessionID })
            return
        }
      }
    })()

    return () => controller.abort()
  },
}
