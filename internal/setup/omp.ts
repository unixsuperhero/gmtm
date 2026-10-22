// GMTM_SETUP_ID=omp
// Managed by gmtm setup. Does not replace matomeru's extension.
import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";

const capture = { bin: "", db: "", server: "", tuios: "" };
// Capture identity is distinct from the native unnamed daemon's empty server.
const daemonServer = capture.server === "@default" ? "" : capture.server;

type Turn = {
  id: string; server: string; host: string; session: string; window: string;
  kind: "agent"; harness: "omp"; conversation_id: string;
  prompt: string; response: string; started_at: number; completed_at: number;
  state: "working" | "done" | "errored" | "interrupted";
};
type Report = {
  state: "idle" | "working" | "needs_input" | "done" | "errored";
  message?: string; kind?: "approval"; if_state?: string;
  activity?: { event: "prompt" | "turn_end"; text: string; target: string };
};

function run(bin: string, args: string[], input?: string): Promise<void> {
  const { promise, resolve, reject } = Promise.withResolvers<void>();
  const env: NodeJS.ProcessEnv = { ...process.env, TUIOS_SERVER: daemonServer };
  delete env.TUIOS_SOCKET;
  const child = spawn(bin, args, {
    env, stdio: [input === undefined ? "ignore" : "pipe", "ignore", "pipe"],
    windowsHide: true, timeout: 10000, killSignal: "SIGKILL",
  });
  let error = "";
  child.stderr?.on("data", chunk => { error = (error + chunk.toString()).slice(-2000); });
  child.once("error", reject);
  child.once("close", code => code === 0 ? resolve() : reject(new Error(error || `${bin} exited ${code}`)));
  child.stdin?.on("error", reject);
  child.stdin?.end(input);
  return promise;
}

export default function (pi: ExtensionAPI) {
  // Activate only in a pane of the explicitly selected daemon.
  if ((process.env.TUIOS_SERVER ?? "") !== daemonServer || !process.env.TUIOS_PANE_ID || !process.env.TUIOS_SESSION) return;
  const session = process.env.TUIOS_SESSION;
  const window = process.env.TUIOS_PANE_ID;
  const main = (ctx: ExtensionContext) => ctx.mode === "tui" && ctx.agent.kind === "main";
  let active: Turn | undefined;
  let pending = Promise.resolve();

  async function save(ctx: ExtensionContext, turn: Turn): Promise<void> {
    try {
      await run(capture.bin, ["--server", capture.server, "--db", capture.db, "capture", "record"], JSON.stringify(turn));
    } catch (error) {
      ctx.ui.notify(`gmtm capture failed: ${error instanceof Error ? error.message : String(error)}`, "warning");
    }
  }
  function report(ctx: ExtensionContext, payload: Report): Promise<void> {
    if (!main(ctx)) return Promise.resolve();
    const params = {
      session, window, harness: "omp", harness_pid: process.pid,
      agent_session_id: ctx.sessionManager.getSessionId(),
      transcript_path: ctx.sessionManager.getSessionFile(), ...payload,
    };
    pending = pending.then(() => run(capture.tuios, ["--server", daemonServer, "call", "set-agent-state", JSON.stringify(params)]))
      .catch(error => { ctx.ui.notify(`gmtm state report failed: ${String(error)}`, "warning"); });
    return pending;
  }
  async function interrupt(ctx: ExtensionContext): Promise<void> {
    if (!active) return;
    const turn = active;
    active = undefined;
    await save(ctx, { ...turn, state: "interrupted", completed_at: Date.now() });
    await report(ctx, { state: "errored", message: "Agent turn interrupted" });
  }
  pi.on("session_start", async (_event, ctx) => {
    if (!main(ctx)) return;
    await report(ctx, { state: ctx.isIdle() ? "idle" : "working" });
  });
  pi.on("session_switch", async (_event, ctx) => {
    if (!main(ctx)) return;
    await interrupt(ctx);
    await report(ctx, { state: ctx.isIdle() ? "idle" : "working" });
  });
  pi.on("session_shutdown", async (_event, ctx) => { if (main(ctx)) await interrupt(ctx); });
  pi.on("before_agent_start", async (event, ctx) => {
    if (!main(ctx)) return;
    if (active && active.conversation_id !== ctx.sessionManager.getSessionId()) await interrupt(ctx);
    if (active) {
      active.prompt += "\n\n" + event.prompt;
      await save(ctx, active);
      return;
    }
    active = {
      id: randomUUID(), server: capture.server, host: "local", session, window,
      kind: "agent", harness: "omp", conversation_id: ctx.sessionManager.getSessionId(),
      prompt: event.prompt, response: "", started_at: Date.now(), completed_at: 0, state: "working",
    };
    await save(ctx, active);
    await report(ctx, { state: "working", activity: { event: "prompt", text: event.prompt.slice(0, 2000), target: `gmtm-turn:${active.id}` } });
  });
  pi.on("agent_start", async (_event, ctx) => { await report(ctx, { state: "working" }); });
  pi.on("agent_end", async (event, ctx) => {
    if (!main(ctx) || !active) return;
    const messages = event.messages.filter(message => message.role === "assistant");
    const last = messages[messages.length - 1];
    const response = messages.map(message => message.content.filter(part => part.type === "text").map(part => part.text).join("\n")).filter(Boolean).join("\n\n");
    if (response) active.response += (active.response ? "\n\n" : "") + response;
    if (event.willContinue) { await save(ctx, active); return; }
    const turn = active;
    active = undefined;
    turn.state = last?.stopReason === "aborted" ? "interrupted" : !last || last.stopReason === "error" ? "errored" : "done";
    if (last?.errorMessage) turn.response += (turn.response ? "\n\n" : "") + last.errorMessage;
    if (!last) turn.response += (turn.response ? "\n\n" : "") + "Agent stopped without a final answer";
    turn.completed_at = Date.now();
    await save(ctx, turn);
    await report(ctx, {
      state: turn.state === "done" ? "done" : "errored",
      message: turn.state === "done" ? undefined : last?.errorMessage || "Agent turn interrupted or failed",
      activity: { event: "turn_end", text: turn.response.slice(0, 2000), target: `gmtm-turn:${turn.id}` },
    });
  });
  pi.on("tool_approval_requested", async (_event, ctx) => {
    await report(ctx, { state: "needs_input", kind: "approval", message: "Tool approval requested" });
  });
  pi.on("tool_approval_resolved", async (_event, ctx) => { await report(ctx, { state: "working", if_state: "needs_input" }); });
}
