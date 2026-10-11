/**
 * Argus gate extension for Pi.
 *
 * Dual-mode, selected at runtime:
 * - Managed mode (ARGUS_MANAGED=1, set by Argus on `pi --mode rpc` children):
 *   tool approvals surface as ctx.ui dialogs, which RPC forwards to Argus as
 *   extension_ui_request records. Argus answers with extension_ui_response.
 *   The beacon client stays off.
 * - Beacon mode (any other pi process with this file globally installed):
 *   the extension dials the Argus node socket (ARGUS_SOCKET) to report
 *   presence and receive remote intents. Without a socket it still gates
 *   tool calls with native dialogs (TUI renders them).
 *
 * Load guard: pi loads this file twice when it is both globally installed and
 * passed with -e (sources "auto" + "cli"). The second factory call registers
 * nothing, so handlers never duplicate (double dialogs would wedge approvals).
 */

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

const g = globalThis as unknown as { __argusGateLoaded?: boolean };

export default function (pi: ExtensionAPI) {
	if (g.__argusGateLoaded) return;
	g.__argusGateLoaded = true;

	const managed = process.env["ARGUS_MANAGED"] === "1";
	const socketPath = process.env["ARGUS_SOCKET"] ?? "";

	pi.on("tool_call", async (event, ctx) => {
		const tool = pi.getAllTools().find((t) => t.name === event.toolName);
		const hints = tool?.annotations;
		const needsApproval =
			hints?.destructiveHint === true ||
			(!hints?.readOnlyHint &&
				((hints?.destructiveHint ?? true) || (hints?.openWorldHint ?? true)));
		if (!needsApproval) return undefined;

		// Beacon mode with a live Argus link: remote decision first.
		if (!managed && socketPath) {
			const decision = await askBeacon(event.toolName, summarizeInput(event.input));
			if (decision === "allow") return undefined;
			if (decision === "deny")
				return { block: true, reason: `${event.toolName} denied by Argus` };
			// fall through to native dialog when Argus is unreachable
		}

		if (!ctx.hasUI) {
			return { block: true, reason: `${event.toolName} needs approval (no UI)` };
		}
		const detail = summarizeInput(event.input);
		const ok = await ctx.ui.confirm(
			`Allow ${event.toolName}?`,
			detail ? `${event.toolName} ${detail}` : event.toolName,
		);
		if (!ok) return { block: true, reason: `${event.toolName} was not approved` };
		return undefined;
	});

	pi.registerCommand("argus-ping", {
		description: "Argus gate status (managed/beacon, socket link)",
		handler: async (_args, ctx) => {
			ctx.ui.notify(
				`argus gate: ${managed ? "managed" : "beacon"}${socketPath ? ` socket=${socketPath}` : ""}`,
				"info",
			);
		},
	});

	// Stashed live context for the beacon hello. Refreshed on every
	// session_start because session replacement invalidates the old one.
	let liveCtx: ExtensionContext | null = null;
	pi.on("session_start", async (_ev, ctx) => {
		liveCtx = ctx;
		if (!managed && socketPath) connectBeacon(pi, socketPath);
	});
	pi.on("session_shutdown", async () => {
		liveCtx = null;
		disconnectBeacon();
	});

	if (!managed && socketPath) {
		beaconIntents.onPrompt = (text, busy) => {
			pi.sendUserMessage(text, busy ? { deliverAs: "steer" } : {});
		};
		beaconIntents.describe = () => {
			const ctx = liveCtx;
			if (!ctx) return null;
			return {
				sessionId: ctx.sessionManager.getSessionId(),
				sessionFile: ctx.sessionManager.getSessionFile() ?? null,
				cwd: ctx.sessionManager.getCwd(),
				model: ctx.model?.id ?? null,
				idle: ctx.isIdle(),
				pid: process.pid,
			};
		};
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

type Decision = "allow" | "deny" | "offline";

const pendingDecisions = new Map<
	string,
	{ resolve: (d: Decision) => void; timer: ReturnType<typeof setTimeout> }
>();

const beaconIntents: {
	onPrompt: ((text: string, busy: boolean) => void) | null;
	describe: (() => unknown) | null;
} = { onPrompt: null, describe: null };

let beaconConn: { write: (s: string) => void; close: () => void } | null = null;
let beaconReady = false;

function askBeacon(toolName: string, detail: string): Promise<Decision> {
	if (!beaconReady || !beaconConn) return Promise.resolve("offline");
	const id = `${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
	return new Promise((resolve) => {
		const timer = setTimeout(() => {
			pendingDecisions.delete(id);
			resolve("offline");
		}, 120_000);
		pendingDecisions.set(id, { resolve, timer });
		beaconConn?.write(JSON.stringify({ type: "approval_request", id, toolName, detail }) + "\n");
	});
}

async function connectBeacon(pi: ExtensionAPI, socketPath: string) {
	void pi;
	if (beaconConn) return;
	const net = await import("node:net");
	const conn = net.createConnection(socketPath);
	let buf = "";
	conn.on("connect", () => {
		beaconReady = true;
		beaconConn = { write: (s: string) => conn.write(s), close: () => conn.end() };
		const info = beaconIntents.describe?.();
		beaconConn.write(JSON.stringify({ type: "hello", ...(info as object) }) + "\n");
	});
	conn.on("data", (chunk: Buffer) => {
		buf += chunk.toString();
		let i: number;
		while ((i = buf.indexOf("\n")) >= 0) {
			const line = buf.slice(0, i);
			buf = buf.slice(i + 1);
			if (line.trim()) handleBeaconLine(line);
		}
	});
	const down = () => {
		beaconReady = false;
		beaconConn = null;
		setTimeout(() => void connectBeacon(pi, socketPath), 3000);
	};
	conn.on("close", down);
	conn.on("error", () => {});
}

function disconnectBeacon() {
	beaconReady = false;
	beaconConn?.close();
	beaconConn = null;
}

function handleBeaconLine(line: string) {
	let msg: any;
	try {
		msg = JSON.parse(line);
	} catch {
		return;
	}
	if (msg?.type === "approval_decision" && typeof msg.id === "string") {
		const p = pendingDecisions.get(msg.id);
		if (!p) return;
		pendingDecisions.delete(msg.id);
		clearTimeout(p.timer);
		p.resolve(msg.decision === "deny" ? "deny" : "allow");
		return;
	}
	if (msg?.type === "intent_prompt" && typeof msg.text === "string") {
		beaconIntents.onPrompt?.(msg.text, msg.busy === true);
	}
}
