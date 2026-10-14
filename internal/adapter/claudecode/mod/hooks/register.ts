import type { EngineInterface, Register } from 'claude-code'

// How long a forwarded event may hold Claude up before going on without argusd.
const EVENT_TIMEOUT_MS = 2000
const RETRY_MIN_MS = 1000
const RETRY_MAX_MS = 15000
// How long a command from argus may take to fail before it is acked as accepted.
const ACK_WINDOW_MS = 1000

export type Command = { id: string; kind: 'prompt' | 'exit'; text: string }

type Env = { tmuxPane: string; tmux: string; autoMode: boolean }

type EventResult = { requestId?: string }

type DecisionResult = { done: boolean; output?: string }

// One permission request's wait for argus; settled once Claude's side decided.
type Ask = {
  requestId?: string
  settled: boolean
  toolName: string
  toolInput: Record<string, unknown>
}

type Decision =
  | { behavior: 'allow'; updatedInput?: Record<string, unknown>; updatedPermissions?: never[] }
  | { behavior: 'deny'; message?: string; interrupt?: true }

let socketPath = ''
let env: Promise<Env> | undefined
// The last transcript path a classic event carried; classic.PreToolUse has none.
let transcriptPath = ''
// Identifies the live poll loop; a loop stops once a newer one has started.
let pollGen = 0
// The permission requests still waiting on argus.
const asks = new Set<Ask>()
// The running main-loop turn, which $.turn.abort takes; '' when idle.
let turnId = ''

export const register: Register = (on, options) => {
  socketPath = String(options.socket ?? '')
  env = undefined

  // The session may move to a new id (/clear, /resume): repoll under it at once.
  on('classic.SessionStart', async ($, e, next) => {
    await forward($, 'SessionStart', e)
    startPolling($)
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.UserPromptSubmit', async ($, e, next) => (await forward($, 'UserPromptSubmit', e), next(e)))
    .catch(($, e, next) => next(e))
  // argusd never reads the tool's output, which can be large.
  on('classic.PostToolUse', async ($, e, next) => (await forward($, 'PostToolUse', withoutResponse(e)), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.PostToolUseFailure', async ($, e, next) => (await forward($, 'PostToolUseFailure', e), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.Notification', async ($, e, next) => (await forward($, 'Notification', e), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.Stop', async ($, e, next) => (await forward($, 'Stop', e), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.SessionEnd', async ($, e, next) => (await forward($, 'SessionEnd', e), next(e)))
    .catch(($, e, next) => next(e))

  // classic.PreToolUse carries the tool call, not the classic stdin shape: rebuild it.
  on('classic.PreToolUse', async ($, e, next) => {
    const { tool, tool_use_id, ...toolInput } = e as Record<string, unknown>
    await forward($, 'PreToolUse', {
      hook_event_name: 'PreToolUse',
      ...(await sessionFields($)),
      tool_name: tool,
      tool_use_id,
      tool_input: toolInput,
    })
    return next(e)
  }).catch(($, e, next) => next(e))

  // The prompt shows in Claude and in argus at once; whichever answers first wins.
  // next(e) is the settings hooks: it resolves with no decision when none decided,
  // and Claude's dialog then shows; a local answer there aborts this hook.
  on('classic.PermissionRequest', async ($, e, next) => {
    const ev = e as Record<string, unknown>
    const ask: Ask = {
      settled: false,
      toolName: String(ev.tool_name ?? ''),
      toolInput: ev.tool_input && typeof ev.tool_input === 'object' && !Array.isArray(ev.tool_input)
        ? (ev.tool_input as Record<string, unknown>)
        : {},
    }
    asks.add(ask)
    try {
      // A local answer aborts this hook: stop waiting on argus, so its lease ends the prompt there.
      next.signal.addEventListener('abort', () => { ask.settled = true })
      const theirs = next(e)
      const mine = askArgus($, e, ask)
      const first = await Promise.race([
        theirs.then(result => ({ result })),
        mine.then(decision => ({ decision })),
      ])
      if ('result' in first && !first.result.decision) {
        // No settings hook decided: Claude's dialog is up; argus answers too.
        const decision = await mine
        return decision ? { ...first.result, decision } : first.result
      }
      if ('result' in first) {
        // A settings hook decided: take the prompt down in argus too.
        await settle($, ask)
        return first.result
      }
      return first.decision ? { decision: first.decision } : theirs
    } finally {
      asks.delete(ask)
    }
  }).catch(($, e, next) => next(e))

  // The tool call's next(e) covers Claude's permission step: when it returns,
  // Claude has decided any request argus still holds for this call.
  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    if (socketPath !== '' && asks.size > 0) await endAsksFor($, e as Record<string, unknown>)
    return result
  }).catch(($, e, next) => next(e))

  // An interrupted turn fires no Stop: report every main-loop turn end as one
  // (a second Stop after an answered turn only re-applies the same idle state).
  on('turn.start', async ($, e, next) => {
    turnId = e.turnId
    return next(e)
  })
  on('turn.complete', async ($, e, next) => {
    const done = await next(e)
    if (!e.agentId) {
      turnId = ''
      await forward($, 'Stop', {
        hook_event_name: 'Stop',
        ...(await sessionFields($)),
        stop_hook_active: false,
      })
    }
    return done
  }).catch(($, e, next) => next(e))

  on('session.start', async ($, e, next) => {
    const started = await next(e)
    startPolling($)
    return started
  })
}

async function post($: EngineInterface, path: string, body: unknown): Promise<string> {
  const res = await $.http.fetch(`http://argus${path}`, {
    method: 'POST',
    socketPath,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`argusd ${path}: ${res.status}`)
  return res.text
}

function readEnv($: EngineInterface): Promise<Env> {
  env ??= Promise.all([
    $.env.get('TMUX_PANE'),
    $.env.get('TMUX'),
    $.env.get('CLAUDE_CODE_ENABLE_AUTO_MODE'),
  ]).then(([tmuxPane, tmux, auto]) => ({
    tmuxPane: tmuxPane ?? '',
    tmux: tmux ?? '',
    autoMode: auto === '1',
  }))
  return env
}

async function sendEvent($: EngineInterface, event: string, payload: object): Promise<EventResult> {
  const p = payload as Record<string, unknown>
  if (typeof p.transcript_path === 'string' && p.transcript_path !== '') transcriptPath = p.transcript_path
  return JSON.parse(await post($, '/claude/event', { event, payload, ...(await readEnv($)) })) as EventResult
}

// Register the permission request with argusd, then wait for the user's answer
// there in requests short enough for $.http.fetch's 30 s limit. Undefined when
// argus has no answer: no socket is configured, argusd is unreachable, or the
// request ended elsewhere.
async function askArgus($: EngineInterface, e: object, ask: Ask): Promise<Decision | undefined> {
  if (socketPath === '') return undefined
  try {
    const { requestId } = await sendEvent($, 'PermissionRequest', e)
    if (!requestId) return undefined
    ask.requestId = requestId
    while (!ask.settled) {
      const r = JSON.parse(await post($, '/claude/decision', { requestId })) as DecisionResult
      if (r.done) return parseDecision(r.output ?? '')
    }
    return undefined
  } catch {
    return undefined
  }
}

// Take down argus's prompt for every open request this tool call settles.
// Mirrors argusd's PostToolUse matching.
async function endAsksFor($: EngineInterface, call: Record<string, unknown>): Promise<void> {
  for (const ask of [...asks]) {
    if (ask.toolName !== call.tool) continue
    const whole = ask.toolName === 'AskUserQuestion' || ask.toolName === 'ExitPlanMode'
    const matches = whole || Object.entries(ask.toolInput).every(
      ([k, v]) => k in call && JSON.stringify(call[k]) === JSON.stringify(v),
    )
    if (matches) await settle($, ask)
  }
}

// Stop waiting on argus for ask and take its prompt down there.
async function settle($: EngineInterface, ask: Ask): Promise<void> {
  ask.settled = true
  if (ask.requestId) await post($, '/claude/cancel', { requestId: ask.requestId }).catch(() => '')
}

// The classic hook fields that name the session, for events rebuilt here.
async function sessionFields($: EngineInterface): Promise<Record<string, string>> {
  const [id, cwd] = await Promise.all([$.session.id(), $.session.cwd()])
  return { session_id: id, transcript_path: transcriptPath, cwd }
}

function withoutResponse(e: object): object {
  const { tool_response: _, ...rest } = e as Record<string, unknown>
  return rest
}

// Forward an event, giving up after EVENT_TIMEOUT_MS so argusd never stalls Claude.
async function forward($: EngineInterface, event: string, payload: object): Promise<void> {
  if (socketPath === '') return
  await Promise.race([
    sendEvent($, event, payload).catch(() => undefined),
    $.clock.sleep(EVENT_TIMEOUT_MS),
  ])
}

function startPolling($: EngineInterface): void {
  if (socketPath === '') return
  pollGen++
  schedulePoll($, 0, RETRY_MIN_MS, pollGen)
}

// A loop whose gen is no longer current still runs the commands its in-flight
// poll already received, then stops.
function schedulePoll($: EngineInterface, delayMs: number, retryMs: number, gen: number): void {
  $.clock.after(delayMs, async () => {
    if (gen !== pollGen) return
    try {
      const out = await post($, '/claude/poll', { sessionId: await $.session.id() })
      const { commands } = JSON.parse(out) as { commands?: Command[] }
      for (const c of commands ?? []) await run($, c)
      if (gen === pollGen) schedulePoll($, 0, RETRY_MIN_MS, gen)
    } catch {
      if (gen === pollGen) schedulePoll($, retryMs, Math.min(retryMs * 2, RETRY_MAX_MS), gen)
    }
  })
}

// Run a command and ack it: argusd fails the send in argus when no ack comes.
// A call resolves only once its turn starts, or a command's panel closes, so a
// call still pending after ACK_WINDOW_MS is acked as accepted (not ran) and
// left to finish.
export async function run($: EngineInterface, c: Command): Promise<void> {
  if (c.kind === 'exit') return exit($, c)
  const failure = await Promise.race([execute($, c), $.clock.sleep(ACK_WINDOW_MS).then(() => PENDING)])
  if (failure === PENDING) {
    await post($, '/claude/ack', { id: c.id }).catch(() => '')
    return
  }
  await post($, '/claude/ack', failure === '' ? { id: c.id, ran: true } : { id: c.id, error: failure }).catch(() => '')
}

const PENDING = Symbol('pending')

// Ends the session at once: a command waits for the running turn, so end that
// first, and ack before Claude exits and takes the ack with it.
async function exit($: EngineInterface, c: Command): Promise<void> {
  if (turnId !== '') await $.turn.abort({ turnId }).catch(() => undefined)
  await post($, '/claude/ack', { id: c.id }).catch(() => '')
  await $.command.run({ command: 'exit' }).catch(err => {
    $.ui.log(`argus: exit failed: ${String(err)}`, { to: 'debug' })
  })
}

// Resolves with the command's failure, or '' once it ran. Claude takes no
// prompt starting with "/", so such text must name a slash command.
async function execute($: EngineInterface, c: Command): Promise<string> {
  let failure = ''
  try {
    const slash = /^\/(\S+)\s*([\s\S]*)$/.exec(c.text)
    if (slash?.[1].includes('/')) {
      failure = `/${slash[1]} is not a slash command, and Claude takes no prompt starting with /`
    } else if (slash) {
      await $.command.run({ command: slash[1], args: slash[2] })
    } else {
      const r = await $.prompt.submit({ text: c.text, asUser: true })
      if (r.drop !== undefined) failure = `prompt dropped: ${r.drop}`
    }
  } catch (err) {
    failure = String(err)
  }
  if (failure !== '') $.ui.log(`argus: ${c.kind} failed: ${failure}`, { to: 'debug' })
  return failure
}

export function parseDecision(output: string): Decision | undefined {
  if (output === '') return undefined
  const d = (JSON.parse(output) as { hookSpecificOutput?: { decision?: Record<string, unknown> } })
    .hookSpecificOutput?.decision
  if (d?.behavior === 'deny') {
    return {
      behavior: 'deny',
      ...(typeof d.message === 'string' && d.message !== '' ? { message: d.message } : {}),
      ...(d.interrupt === true ? { interrupt: true as const } : {}),
    }
  }
  if (d?.behavior === 'allow') {
    return {
      behavior: 'allow',
      ...(d.updatedInput ? { updatedInput: d.updatedInput as Record<string, unknown> } : {}),
      ...(d.updatedPermissions ? { updatedPermissions: d.updatedPermissions as never[] } : {}),
    }
  }
  return undefined
}
