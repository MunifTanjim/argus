// argus-gate: managed by argus; edits are overwritten
/**
 * Argus gate extension for Pi.
 *
 * Dual-mode, selected at runtime:
 * - Managed mode (ARGUS_MANAGED=1, set by Argus on `pi --mode rpc` children):
 *   the gate only marks the process. RPC already forwards every dialog, and
 *   the beacon client stays off.
 * - Beacon mode (any other pi process with this file globally installed):
 *   the extension opens a beacon connection on the Argus node's mod socket
 *   (SOCKET_PATH) for presence, remote intents, and forwarding the dialogs
 *   other extensions open.
 *
 * The gate adds no approvals: a pi process with it behaves like one without,
 * apart from the beacon connection.
 *
 * Load guard: pi loads this file twice when it is both globally installed and
 * passed with -e (sources "auto" + "cli"). The second factory call registers
 * nothing, so handlers never duplicate. A session change (/new, /resume, fork,
 * /reload) shuts the runtime down and loads extensions again, so
 * session_shutdown clears the guard and stops the old beacon. It also leaves
 * the old session id on globalThis: the next hello names it, so Argus keeps the
 * card across the switch.
 */

import type { ExtensionAPI, ExtensionContext, ExtensionUIDialogOptions } from "@earendil-works/pi-coding-agent";

// Argus writes its mod socket path here when it installs this file globally.
const SOCKET_PATH = "";

const g = globalThis as unknown as { __argusGateLoaded?: boolean; __argusPrevSessionId?: string };

export default function (pi: ExtensionAPI) {
	if (g.__argusGateLoaded) return;
	g.__argusGateLoaded = true;
	let previousSessionId = g.__argusPrevSessionId;
	delete g.__argusPrevSessionId;

	const managed = process.env["ARGUS_MANAGED"] === "1";
	const beacon = !managed && SOCKET_PATH !== "";

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
		if (beacon) {
			startBeacon();
			patchDialogs(ctx);
		}
	});
	pi.on("session_shutdown", async () => {
		if (beacon) g.__argusPrevSessionId = liveCtx?.sessionManager.getSessionId();
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
		// Dialogs patchDialogs cannot forward (editor, custom, opened offline)
		// reach extensions only as kind and title. Argus shows that one waits in pi.
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
		// Only the first hello names the previous session; describe runs only
		// to build a hello.
		beaconIntents.describe = () => {
			const ctx = liveCtx;
			if (!ctx) return null;
			const prev = previousSessionId;
			previousSessionId = undefined;
			return {
				...(prev ? { previousSessionId: prev } : {}),
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

// -- Beacon client (beacon mode only; managed sessions never reach here) --

type DialogAnswer = { value?: string; confirmed?: boolean; cancelled?: boolean };

const pendingDialogs = new Map<string, (r: DialogAnswer | null) => void>();

// Forwarded dialogs still open in pi. Each new connection gets their requests
// again, so a dialog outlives an Argus restart.
const openDialogs = new Map<string, { request: object; answer: (r: DialogAnswer) => void }>();

// Titles of the dialogs patchDialogs forwarded and that are open in pi; Argus
// shows those already.
const ownDialogs = new Set<string>();

const beaconIntents: {
	onPrompt: ((text: string, busy: boolean) => void) | null;
	onRename: ((name: string) => void) | null;
	onShell: ((id: string, text: string) => void) | null;
	onCompact: ((instructions: string) => void) | null;
	describe: (() => unknown) | null;
} = { onPrompt: null, onRename: null, onShell: null, onCompact: null, describe: null };

let beaconConn: { write: (s: string) => void; close: () => void } | null = null;

const dialogMark = Symbol.for("argus.dialogsPatched");

type DialogMethod = "select" | "confirm" | "input";
type DialogFn = (title: string, ...rest: any[]) => Promise<any>;

// Patches the shared ctx.ui so other extensions' dialogs reach Argus too.
// pi 1.x gives every extension the same runner.uiContext object.
function patchDialogs(ctx: ExtensionContext): void {
	if (!ctx.hasUI) return;
	const ui = ctx.ui as unknown as Record<string | symbol, unknown>;
	if (ui[dialogMark]) return;
	const methods: DialogMethod[] = ["select", "confirm", "input"];
	if (!methods.every((m) => typeof ui[m] === "function")) return;
	const originals = new Map<DialogMethod, DialogFn>();
	const wrappers = new Map<DialogMethod, DialogFn>();
	for (const m of methods) {
		const original = (ui[m] as DialogFn).bind(ui);
		originals.set(m, original);
		wrappers.set(m, forwardDialog(m, original));
	}
	const saved = new Map(methods.map((m) => [m, ui[m]]));
	try {
		for (const m of methods) ui[m] = wrappers.get(m);
	} catch {
		// frozen object: fall through to the check below
	}
	if (methods.every((m) => ui[m] === wrappers.get(m))) {
		ui[dialogMark] = true;
		return;
	}
	for (const m of methods) {
		try {
			ui[m] = saved.get(m);
		} catch {
			// nothing left to restore
		}
	}
}

function forwardDialog(method: DialogMethod, original: DialogFn): DialogFn {
	return async (title: string, ...args: any[]) => {
		if (!beaconConn) return original(title, ...args);
		// select(title, options, opts), confirm(title, message, opts), input(title, placeholder, opts)
		const optsAt = 1;
		const opts = args[optsAt] as ExtensionUIDialogOptions | undefined;
		const id = `${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
		const ours = new AbortController();
		const signal = AbortSignal.any([opts?.signal, ours.signal].filter((s): s is AbortSignal => !!s));
		const callArgs = [...args];
		callArgs[optsAt] = { ...opts, signal };
		ownDialogs.add(title);
		const request = {
			type: "ui_request",
			id,
			method,
			title,
			...(method === "confirm" ? { message: args[0] } : {}),
			...(method === "select" ? { options: args[0] } : {}),
		};
		// A dropped link resolves the pending entry with null: until the next
		// connection asks again, only the local dialog can answer.
		const argus = new Promise<{ argus: true; r: DialogAnswer }>((resolve) => {
			openDialogs.set(id, { request, answer: (r) => resolve({ argus: true, r }) });
		});
		awaitArgus(id);
		beaconSend(request);
		let localDone: Promise<void> = Promise.resolve();
		try {
			const local = original(title, ...callArgs).then((value) => ({ argus: false as const, value }));
			// pi ends the prompt after the local dialog settles, so the title stays
			// marked until then.
			localDone = local.then(
				() => {},
				() => {},
			);
			const first = await Promise.race([local, argus]);
			if (!first.argus) return first.value;
			ours.abort();
			await localDone;
			if (method === "confirm") return first.r.confirmed === true;
			return first.r.cancelled ? undefined : first.r.value;
		} finally {
			openDialogs.delete(id);
			if (pendingDialogs.delete(id)) beaconSend({ type: "ui_settled", id });
			void localDone.then(() => ownDialogs.delete(title));
		}
	};
}

function awaitArgus(id: string) {
	pendingDialogs.set(id, (r) => {
		if (r) openDialogs.get(id)?.answer(r);
	});
}

function resendDialogs() {
	for (const [id, { request }] of openDialogs) {
		awaitArgus(id);
		beaconSend(request);
	}
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
		resendDialogs();
		startHeartbeat();
		if (head.length) feed(head);
		conn.on("data", feed);
		conn.on("close", () => {
			if (beaconConn !== self && beaconConn !== null) return;
			beaconConn = null;
			stopHeartbeat();
			for (const resolve of pendingDialogs.values()) resolve(null);
			pendingDialogs.clear();
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
	if (msg?.type === "ui_response" && typeof msg.id === "string") {
		const resolve = pendingDialogs.get(msg.id);
		if (!resolve) return;
		pendingDialogs.delete(msg.id);
		resolve({
			value: typeof msg.value === "string" ? msg.value : undefined,
			confirmed: msg.confirmed === true,
			cancelled: msg.cancelled === true,
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
