/**
 * Argus gate extension for Pi.
 *
 * Dual-mode, selected at runtime:
 * - Managed mode (ARGUS_MANAGED=1, set by Argus on `pi --mode rpc` children):
 *   tool approvals surface as ctx.ui dialogs, which RPC forwards to Argus as
 *   extension_ui_request records. Argus answers with extension_ui_response.
 *   The beacon client stays off.
 * - Beacon mode (any other pi process with this file globally installed):
 *   the extension opens a beacon connection on the Argus node's mod socket
 *   (SOCKET_PATH) to report presence and receive remote intents. Without a
 *   socket it still gates tool calls with native dialogs (TUI renders them).
 *
 * Load guard: pi loads this file twice when it is both globally installed and
 * passed with -e (sources "auto" + "cli"). The second factory call registers
 * nothing, so handlers never duplicate (double dialogs would wedge approvals).
 * A session change (/new, /resume, fork, /reload) shuts the runtime down and
 * loads extensions again, so session_shutdown clears the guard and stops the
 * old beacon.
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

// Argus writes its mod socket path here when it installs this file globally.
const SOCKET_PATH = "";

// Marks the managed-mode approval dialog for Argus (approvalMarker in Go). It
// is an input dialog because RPC turns a confirm answer into a bare boolean,
// and Argus answers with JSON that carries the deny reason.
const APPROVAL_MARKER = "argus:approval:";

const g = globalThis as unknown as { __argusGateLoaded?: boolean };

export default function (pi: ExtensionAPI) {
	if (g.__argusGateLoaded) return;
	g.__argusGateLoaded = true;

	const managed = process.env["ARGUS_MANAGED"] === "1";
	const beacon = !managed && SOCKET_PATH !== "";

	pi.on("tool_call", async (event, ctx) => {
		const tool = pi.getAllTools().find((t) => t.name === event.toolName);
		const hints = tool?.annotations;
		const needsApproval =
			hints?.destructiveHint === true ||
			(!hints?.readOnlyHint &&
				((hints?.destructiveHint ?? true) || (hints?.openWorldHint ?? true)));
		if (!needsApproval) return undefined;

		const detail = summarizeInput(event.input);
		const label = detail ? `${event.toolName} ${detail}` : event.toolName;
		const denied = (reason?: string) => ({ block: true, reason: reason || `${event.toolName} denied by Argus` });
		const title = `Allow ${event.toolName}?`;
		if (managed) {
			const answer = parseApproval(await ctx.ui.input(title, APPROVAL_MARKER + label));
			return answer.allow ? undefined : denied(answer.reason);
		}
		// The prompt shows in pi and in Argus at once; whichever answers first
		// wins and takes the other down.
		const remote = beacon ? askBeacon(event.toolName, detail) : null;
		if (!ctx.hasUI) {
			const decision: Decision = remote ? await remote.decision : { kind: "offline" };
			if (decision.kind === "allow") return undefined;
			if (decision.kind === "deny") return denied(decision.reason);
			return { block: true, reason: `${event.toolName} needs approval (no UI)` };
		}
		const dialog = new AbortController();
		ownDialogs.add(title);
		const local = ctx.ui
			.confirm(title, label, { signal: dialog.signal })
			.then((ok) => ({ argus: false, ok, reason: undefined }))
			.finally(() => ownDialogs.delete(title));
		// Offline leaves only the local dialog to answer.
		const argus = remote
			? remote.decision.then((d) =>
					d.kind === "offline"
						? new Promise<never>(() => {})
						: { argus: true, ok: d.kind === "allow", reason: d.reason },
				)
			: new Promise<never>(() => {});
		const first = await Promise.race([local, argus]);
		if (first.argus) dialog.abort();
		else remote?.settle();
		if (first.ok) return undefined;
		return first.argus ? denied(first.reason) : { block: true, reason: `${event.toolName} was not approved` };
	});

	pi.registerCommand("argus-ping", {
		description: "Argus gate status (managed/beacon, socket link)",
		handler: async (_args, ctx) => {
			ctx.ui.notify(
				`argus gate: ${managed ? "managed" : "beacon"}${SOCKET_PATH ? ` socket=${SOCKET_PATH}` : ""}`,
				"info",
			);
		},
	});

	// Stashed live context for the beacon hello. Refreshed on every
	// session_start because session replacement invalidates the old one.
	let liveCtx: ExtensionContext | null = null;
	pi.on("session_start", async (_ev, ctx) => {
		liveCtx = ctx;
		if (beacon) startBeacon();
	});
	pi.on("session_shutdown", async () => {
		liveCtx = null;
		stopBeacon();
		g.__argusGateLoaded = false;
	});

	if (beacon) {
		// Lifecycle forwarding: the node has no RPC stdout for foreign
		// sessions, so status travels over the socket. Managed sessions skip
		// this — their events already flow over RPC.
		pi.on("agent_start", async () => {
			beaconSend({ type: "event", name: "agent_start" });
		});
		pi.on("agent_settled", async (_ev, ctx) => {
			// A run that ended with steers left over (aborted, say) drops them.
			if (queued.length && !ctx.hasPendingMessages()) setQueued([]);
			beaconSend({ type: "event", name: "agent_settled" });
		});
		// Argus can neither show nor answer another extension's dialog: pi tells
		// extensions only its kind and title. Argus shows that one waits in pi.
		const relayPrompt = (open: boolean) => async (ev: { title?: string }) => {
			const title = ev.title ?? "";
			if (!ownDialogs.has(title)) beaconSend({ type: "event", name: "ui_prompt", open, title });
		};
		pi.on("ui_prompt_start", relayPrompt(true));
		pi.on("ui_prompt_end", relayPrompt(false));
		pi.on("session_info_changed", async (ev) => {
			beaconSend({ type: "event", name: "session_info_changed", displayName: ev.name ?? null });
		});
		pi.on("model_select", async (ev) => {
			beaconSend({ type: "event", name: "model", ...modelInfo(ev.model, pi.getThinkingLevel()) });
		});
		pi.on("thinking_level_select", async (ev, ctx) => {
			beaconSend({ type: "event", name: "model", ...modelInfo(ctx.model, ev.level) });
		});
		// pi shows extensions whether prompts wait, not which: the queue Argus
		// shows is the prompts it sent while pi was busy, until pi takes each.
		let queued: string[] = [];
		const setQueued = (next: string[]) => {
			queued = next;
			beaconSend({ type: "event", name: "queue", items: queued });
		};
		pi.on("message_start", async (ev) => {
			if (ev.message.role !== "user" || !queued.length) return;
			const text = messageText(ev.message.content);
			const i = queued.indexOf(text);
			if (i >= 0) setQueued(queued.filter((_, j) => j !== i));
		});
		// Like pi's own editor, a prompt runs extension commands and expands
		// prompt templates and skills. A command runs at once, so it never waits.
		beaconIntents.onPrompt = (text, busy) => {
			pi.sendUserMessage(text, { expandPromptTemplates: true, ...(busy ? { deliverAs: "steer" } : {}) });
			if (busy && !text.startsWith("/")) setQueued([...queued, text]);
		};
		beaconIntents.onRename = (name) => pi.setSessionName(name);
		beaconIntents.onCompact = (instructions) => liveCtx?.compact(instructions ? { customInstructions: instructions } : undefined);
		pi.on("session_before_compact", async () => {
			beaconSend({ type: "event", name: "compaction", open: true });
		});
		const compactionEnded = async () => {
			beaconSend({ type: "event", name: "compaction", open: false });
		};
		pi.on("session_compact", compactionEnded);
		pi.on("session_compact_failed", compactionEnded);
		// pi gives extensions no way to submit "!" input, so the gate stages it in
		// the editor and Argus presses Enter in the pane. The user's draft waits
		// aside until pi runs the command.
		let staged: { text: string; draft: string; timer: ReturnType<typeof setTimeout> } | null = null;
		const putDraftBack = (ran: boolean) => {
			const s = staged;
			if (!s) return;
			staged = null;
			clearTimeout(s.timer);
			// Without a run, the editor holds the command only if Enter never
			// landed or pi refused it; anything else is the user's new input.
			if (liveCtx && (ran || liveCtx.ui.getEditorText() === s.text)) liveCtx.ui.setEditorText(s.draft);
		};
		beaconIntents.onShell = (id, text) => {
			const ctx = liveCtx;
			if (!ctx?.hasUI) {
				beaconSend({ type: "shell_ready", id, ok: false, reason: "the session has no editor" });
				return;
			}
			putDraftBack(false);
			const draft = ctx.ui.getEditorText();
			ctx.ui.setEditorText(text);
			staged = { text, draft, timer: setTimeout(() => putDraftBack(false), 3000) };
			beaconSend({ type: "shell_ready", id, ok: true });
		};
		pi.on("user_bash", async (ev) => {
			const excluded = staged?.text.startsWith("!!") ?? false;
			const command = staged?.text.slice(excluded ? 2 : 1).trim();
			if (staged && ev.command === command && ev.excludeFromContext === excluded) putDraftBack(true);
			return undefined;
		});
		// pi clears the editor before it switches; the draft carries over to the
		// new session.
		pi.on("session_before_switch", async (ev) => {
			if (ev.reason === "new" && staged?.text === "/new") putDraftBack(true);
			return undefined;
		});
		beaconIntents.describe = () => {
			const ctx = liveCtx;
			if (!ctx) return null;
			return {
				sessionId: ctx.sessionManager.getSessionId(),
				sessionFile: ctx.sessionManager.getSessionFile() ?? null,
				cwd: ctx.sessionManager.getCwd(),
				...modelInfo(ctx.model, pi.getThinkingLevel()),
				idle: ctx.isIdle(),
				pid: process.pid,
			};
		};
	}
}

function modelInfo(model: { id: string; reasoning?: boolean } | undefined, thinkingLevel: string) {
	return { model: model?.id ?? null, reasoning: model?.reasoning === true, thinkingLevel };
}

function messageText(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.map((c) => (c && typeof c === "object" && (c as { type?: string }).type === "text" ? String((c as { text?: unknown }).text ?? "") : ""))
		.join("");
}

function parseApproval(answer: string | undefined): { allow: boolean; reason?: string } {
	try {
		const a = JSON.parse(answer ?? "");
		return { allow: a?.allow === true, reason: typeof a?.reason === "string" ? a.reason : undefined };
	} catch {
		return { allow: false };
	}
}

function summarizeInput(input: unknown): string {
	if (!input || typeof input !== "object") return "";
	const o = input as Record<string, unknown>;
	for (const k of ["command", "path", "filePath", "pattern", "description"]) {
		const v = o[k];
		if (typeof v === "string" && v) return v.split("\n")[0]?.slice(0, 200) ?? "";
	}
	return "";
}

// -- Beacon client (beacon mode only; managed sessions never reach here) --

type Decision = { kind: "allow" | "deny" | "offline"; reason?: string };

const pendingDecisions = new Map<string, (d: Decision) => void>();

// Titles of the gate's own approval dialogs open in pi; Argus shows those as
// approvals already.
const ownDialogs = new Set<string>();

const beaconIntents: {
	onPrompt: ((text: string, busy: boolean) => void) | null;
	onRename: ((name: string) => void) | null;
	onShell: ((id: string, text: string) => void) | null;
	onCompact: ((instructions: string) => void) | null;
	describe: (() => unknown) | null;
} = { onPrompt: null, onRename: null, onShell: null, onCompact: null, describe: null };

let beaconConn: { write: (s: string) => void; close: () => void } | null = null;

// askBeacon parks an approval in Argus. The decision resolves "offline" when
// no link is up or the link drops; settle takes the request down in Argus
// once pi answered it.
function askBeacon(toolName: string, detail: string): { decision: Promise<Decision>; settle: () => void } {
	if (!beaconConn) return { decision: Promise.resolve({ kind: "offline" }), settle: () => {} };
	const id = `${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
	const decision = new Promise<Decision>((resolve) => {
		pendingDecisions.set(id, resolve);
		beaconSend({ type: "approval_request", id, toolName, detail });
	});
	const settle = () => {
		if (!pendingDecisions.delete(id)) return;
		beaconSend({ type: "approval_settled", id });
	};
	return { decision, settle };
}

// The beacon runs between session_start and session_shutdown. A stopped
// beacon never reconnects, so a replaced runtime's module goes quiet.
let beaconStopped = true;
let beaconConnecting = false;
let retryTimer: ReturnType<typeof setTimeout> | null = null;

function startBeacon() {
	beaconStopped = false;
	void connectBeacon();
}

function stopBeacon() {
	beaconStopped = true;
	if (retryTimer) clearTimeout(retryTimer);
	retryTimer = null;
	beaconConn?.close();
	beaconConn = null;
}

async function connectBeacon() {
	if (beaconStopped || beaconConn || beaconConnecting) return;
	beaconConnecting = true;
	const http = await import("node:http");
	const req = http.request({
		socketPath: SOCKET_PATH,
		path: "/pi/beacon",
		headers: { Connection: "Upgrade", Upgrade: "argus-beacon" },
	});
	const failed = () => {
		beaconConnecting = false;
		scheduleRetry();
	};
	req.on("upgrade", (_res, conn, head) => {
		beaconConnecting = false;
		if (beaconStopped) {
			conn.destroy();
			return;
		}
		let buf = "";
		const feed = (chunk: Buffer) => {
			buf += chunk.toString();
			let i: number;
			while ((i = buf.indexOf("\n")) >= 0) {
				const line = buf.slice(0, i);
				buf = buf.slice(i + 1);
				if (line.trim()) handleBeaconLine(line);
			}
		};
		const self = { write: (s: string) => conn.write(s), close: () => conn.end() };
		beaconConn = self;
		const info = beaconIntents.describe?.();
		beaconSend({ type: "hello", ...(info as object) });
		startHeartbeat();
		if (head.length) feed(head);
		conn.on("data", feed);
		conn.on("close", () => {
			if (beaconConn !== self && beaconConn !== null) return;
			beaconConn = null;
			stopHeartbeat();
			for (const resolve of pendingDecisions.values()) resolve({ kind: "offline" });
			pendingDecisions.clear();
			scheduleRetry();
		});
		conn.on("error", () => {});
	});
	req.on("response", (res) => {
		res.resume();
		failed();
	});
	req.on("error", failed);
	req.end();
}

function scheduleRetry() {
	if (beaconStopped || retryTimer) return;
	retryTimer = setTimeout(() => {
		retryTimer = null;
		void connectBeacon();
	}, 3000);
}

let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
function startHeartbeat() {
	stopHeartbeat();
	heartbeatTimer = setInterval(() => {
		beaconSend({ type: "heartbeat" });
	}, 30_000);
}
function stopHeartbeat() {
	if (heartbeatTimer) clearInterval(heartbeatTimer);
	heartbeatTimer = null;
}

function beaconSend(obj: unknown) {
	beaconConn?.write(JSON.stringify(obj) + "\n");
}

function handleBeaconLine(line: string) {
	let msg: any;
	try {
		msg = JSON.parse(line);
	} catch {
		return;
	}
	if (msg?.type === "approval_decision" && typeof msg.id === "string") {
		const resolve = pendingDecisions.get(msg.id);
		if (!resolve) return;
		pendingDecisions.delete(msg.id);
		resolve({
			kind: msg.decision === "deny" ? "deny" : "allow",
			reason: typeof msg.reason === "string" ? msg.reason : undefined,
		});
		return;
	}
	if (msg?.type === "intent_prompt" && typeof msg.text === "string") {
		beaconIntents.onPrompt?.(msg.text, msg.busy === true);
	} else if (msg?.type === "intent_rename" && typeof msg.name === "string") {
		beaconIntents.onRename?.(msg.name);
	} else if (msg?.type === "intent_shell" && typeof msg.id === "string" && typeof msg.text === "string") {
		beaconIntents.onShell?.(msg.id, msg.text);
	} else if (msg?.type === "intent_compact") {
		beaconIntents.onCompact?.(typeof msg.instructions === "string" ? msg.instructions : "");
	}
}
