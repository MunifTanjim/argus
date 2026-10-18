import type { EngineInterface, Register } from 'claude-code'

// How long a forwarded event may hold Claude up before going on without argusd.
const EVENT_TIMEOUT_MS = 2000
const RETRY_MIN_MS = 1000
const RETRY_MAX_MS = 15000
// How long a command from argus may take to fail before it is acked as accepted.
const ACK_WINDOW_MS = 1000
// How long a fill's saved draft waits for argus's Enter to submit the text.
const DRAFT_TTL_MS = 5000
// How long Claude takes to read a typed "!" and turn bash mode on.
const BANG_MS = 300
// How long a bash mode command's Enter may take to submit it.
const SUBMIT_WAIT_MS = 2000
const SUBMIT_POLL_MS = 25
// A shell command's ack window: it outlasts both waits, so a failure is acked.
const SHELL_ACK_WINDOW_MS = 4000
// The statuses a task notification reports for a task that ended.
const FINISHED = new Set(['completed', 'failed', 'killed'])
// How much of a shell's command argusd keeps: clients show one short line.
const COMMAND_MAX = 200
// The failure a fill acks when a dialog holds the keys (modbridge.FailBusy).
const FAIL_BUSY = 'busy'

export type Command = { id: string; kind: 'prompt' | 'exit' | 'fill' | 'shell'; text: string }

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
// The model and effort last reported to argusd, so steps report only a change.
let lastModel = ''
let lastEffort: string | undefined
// The text a fill put in Claude's input box and the draft it replaced, until
// argus's Enter submits the text.
let filled: { text: string; draft: string; at: number } | undefined
// Fills acked as accepted, not filled: argus sends no Enter for them.
const abandoned = new Set<string>()
// The main loop's running background shells, by task id, in start order.
const shells = new Map<string, Shell>()

type Shell = { id: string; tool_use_id: string; command: string; desc: string }

export const register: Register = (on, options) => {
  socketPath = String(options.socket ?? '')
  env = undefined
  lastModel = ''
  lastEffort = undefined
  filled = undefined
  abandoned.clear()
  shells.clear()

  // The session may move to a new id (/clear, /resume): repoll under it at once.
  on('classic.SessionStart', async ($, e, next) => {
    // A shell belongs to the session that started it.
    const { source } = e as Record<string, unknown>
    if (source === 'clear' || source === 'resume') shells.clear()
    await forward($, 'SessionStart', e)
    startPolling($)
    await resendState($)
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.UserPromptSubmit', async ($, e, next) => (await forward($, 'UserPromptSubmit', e), next(e)))
    .catch(($, e, next) => next(e))
  // argus's Enter submits a fill: put back the draft it replaced. Matched before
  // the settings hooks beneath run, so a slow hook cannot expire it.
  on('prompt.submit', async ($, e, next) => {
    const draft = await takeDraft($, e.text)
    const result = await next(e)
    if (draft !== undefined) await $.prompt.fill({ text: draft, mode: 'replace' }).catch(() => undefined)
    return result
  })
    .catch(($, e, next) => next(e))
  // argusd never reads the tool's output, which can be large.
  on('classic.PostToolUse', async ($, e, next) => {
    const changed = trackShell(e as Record<string, unknown>)
    await Promise.all([forward($, 'PostToolUse', withoutResponse(e)), changed && reportShells($)])
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.PostToolUseFailure', async ($, e, next) => (await forward($, 'PostToolUseFailure', e), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.Notification', async ($, e, next) => (await forward($, 'Notification', e), next(e)))
    .catch(($, e, next) => next(e))
  // background_tasks only drops shells, so a subagent's shell never joins.
  on('classic.Stop', async ($, e, next) => {
    const tasks = (e as Record<string, unknown>).background_tasks
    let changed = false
    if (Array.isArray(tasks) && shells.size > 0) {
      const listed = new Set(tasks
        .map(t => t as Record<string, unknown>)
        .filter(t => !FINISHED.has(String(t.status)))
        .map(t => String(t.id)))
      changed = dropShells([...shells.keys()].filter(id => !listed.has(id)))
    }
    await Promise.all([forward($, 'Stop', e), changed && reportShells($)])
    return next(e)
  })
    .catch(($, e, next) => next(e))
  // A model's or a tool's row can quote a notification without being one.
  on('session.append', async ($, e, next) => {
    if (shells.size > 0 && !e.agentId && e.origin.kind !== 'model' && e.origin.kind !== 'tool' &&
      dropShells(notifiedTasks(e.message.content))) await reportShells($)
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.SessionEnd', async ($, e, next) => (await forward($, 'SessionEnd', e), next(e)))
    .catch(($, e, next) => next(e))
  on('classic.SubagentStart', async ($, e, next) => {
    const { agent_id, agent_type } = e as Record<string, unknown>
    await reportSubagents($, { started: { id: String(agent_id ?? ''), type: String(agent_type ?? '') } })
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.SubagentStop', async ($, e, next) => {
    await reportSubagents($, { stopped: String((e as Record<string, unknown>).agent_id ?? '') })
    return next(e)
  })
    .catch(($, e, next) => next(e))
  on('classic.PostModelSwitch', async ($, e, next) => {
    await reportModel($, String((e as Record<string, unknown>).to_model ?? ''))
    return next(e)
  })
    .catch(($, e, next) => next(e))

  // No effort, or a numeric one (a budget, not a level), reports ''.
  on('turn.step', async function* ($, e, next) {
    if (!e.agentId) await reportModel($, e.model, typeof e.effort === 'string' ? e.effort : '')
    return yield* next(e)
  })

  // A rename typed in Claude reaches argus once it has run. One argus sends runs
  // through this mod's own $.command.run, which this hook does not see: execute
  // reports that one.
  on('command.run', { command: 'rename' }, async ($, e, next) => {
    const result = await next(e)
    void reportRename($, e.args)
    return result
  })
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
      // Every turn end re-sends both sets, so one that reached argusd out of
      // order is corrected.
      await Promise.all([reportSubagents($), reportShells($)])
    }
    return done
  }).catch(($, e, next) => next(e))

  on('session.start', async ($, e, next) => {
    const started = await next(e)
    startPolling($)
    await reportSubagents($)
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

// Tell argus a /rename ran, so its card follows. A bare /rename names nothing
// to tell.
async function reportRename($: EngineInterface, args: string): Promise<void> {
  const name = args.trim()
  if (name !== '') await forward($, 'SessionRename', { ...(await sessionFields($)), name })
}

// Report the session's live model to argusd when it changed. effort is left
// out when unknown, keeping argusd's last one. Never throws.
async function reportModel($: EngineInterface, model: string, effort?: string): Promise<void> {
  if (model === '' || (model === lastModel && (effort === undefined || effort === lastEffort))) return
  lastModel = model
  if (effort !== undefined) lastEffort = effort
  try {
    await forward($, 'ModelInfo', {
      hook_event_name: 'ModelInfo',
      ...(await sessionFields($)),
      model,
      ...(effort === undefined ? {} : { effort }),
    })
  } catch {
    // argusd hears of it on the next change or reconnect
  }
}

// argusd replaces its set with this one. started and stopped fix up the hook's
// own agent, which $.agent.list may not show yet or still show. Never throws.
async function reportSubagents(
  $: EngineInterface,
  change: { started?: { id: string; type: string }; stopped?: string } = {},
): Promise<void> {
  try {
    const listed = await $.agent.list()
    const subagents = listed
      .filter(a => ['pending', 'running', 'waiting'].includes(a.status) && !a.teammateId && !a.parentId && a.id !== change.stopped)
      .map(a => ({ id: a.id, type: a.type, ...(a.name ? { name: a.name } : {}), desc: a.description }))
    const { started } = change
    if (started && !listed.some(a => a.id === started.id)) {
      subagents.push({ id: started.id, type: started.type, desc: '' })
    }
    await forward($, 'Subagents', { hook_event_name: 'Subagents', ...(await sessionFields($)), subagents })
  } catch {
    // argusd hears of it on the next event
  }
}

// Applies a main-loop tool call to the shell set; true when the set changed.
function trackShell(e: Record<string, unknown>): boolean {
  if (e.agent_id) return false
  const input = (e.tool_input ?? {}) as Record<string, unknown>
  const response = (e.tool_response ?? {}) as Record<string, unknown>
  switch (e.tool_name) {
    case 'Bash': {
      const id = response.backgroundTaskId
      if (typeof id !== 'string' || id === '') return false
      shells.set(id, {
        id,
        tool_use_id: String(e.tool_use_id ?? ''),
        command: String(input.command ?? '').split('\n', 1)[0].slice(0, COMMAND_MAX),
        desc: String(input.description ?? ''),
      })
      return true
    }
    case 'KillShell':
      return shells.delete(String(input.shell_id ?? ''))
    case 'TaskStop':
      return shells.delete(String(response.task_id ?? input.task_id ?? input.shell_id ?? ''))
  }
  return false
}

// Deletes ids from the shell set; true when any was held.
function dropShells(ids: Iterable<string>): boolean {
  let changed = false
  for (const id of ids) changed = shells.delete(id) || changed
  return changed
}

// The task ids of the <task-notification>s in a row.
function notifiedTasks(content: unknown): string[] {
  const parts = typeof content === 'string' ? [content]
    : Array.isArray(content) ? content.flatMap(b => {
      const { type, text } = b as { type?: string; text?: unknown }
      return type === 'text' ? [String(text ?? '')] : []
    })
    : []
  return parts.flatMap(t => t.includes('<task-notification>')
    ? [...t.matchAll(/<task-notification>[\s\S]*?<task-id>([^<]+)<\/task-id>/g)].map(m => m[1])
    : [])
}

// argusd replaces its set with this one. Never throws.
async function reportShells($: EngineInterface): Promise<void> {
  try {
    await forward($, 'Shells', { hook_event_name: 'Shells', ...(await sessionFields($)), shells: [...shells.values()] })
  } catch {
    // argusd hears of it on the next change or reconnect
  }
}

// Send argusd the live model and subagents it keeps in memory again, unchanged
// or not. Never throws.
async function resendState($: EngineInterface): Promise<void> {
  lastModel = ''
  lastEffort = undefined
  await Promise.all([
    $.session.model().catch(() => '').then(model => reportModel($, model)),
    reportSubagents($),
    reportShells($),
  ])
}

function startPolling($: EngineInterface): void {
  if (socketPath === '') return
  pollGen++
  schedulePoll($, 0, RETRY_MIN_MS, pollGen)
}

// A loop whose gen is no longer current still runs the commands its in-flight
// poll already received, then stops.
// A poll that succeeds after a failure means argusd may have restarted, losing
// the live model and subagents it kept in memory: report them again.
function schedulePoll($: EngineInterface, delayMs: number, retryMs: number, gen: number, failed = false): void {
  $.clock.after(delayMs, async () => {
    if (gen !== pollGen) return
    try {
      const out = await post($, '/claude/poll', { sessionId: await $.session.id() })
      const { commands } = JSON.parse(out) as { commands?: Command[] }
      if (failed) await resendState($)
      for (const c of commands ?? []) await run($, c)
      if (gen === pollGen) schedulePoll($, 0, RETRY_MIN_MS, gen)
    } catch {
      if (gen === pollGen) schedulePoll($, retryMs, Math.min(retryMs * 2, RETRY_MAX_MS), gen, true)
    }
  })
}

// Run a command and ack it: argusd fails the send in argus when no ack comes.
// A call resolves only once its turn starts, or a command's panel closes, so a
// call still pending after ACK_WINDOW_MS is acked as accepted (not ran) and
// left to finish.
export async function run($: EngineInterface, c: Command): Promise<void> {
  if (c.kind === 'exit') return exit($, c)
  const window = c.kind === 'shell' ? SHELL_ACK_WINDOW_MS : ACK_WINDOW_MS
  const failure = await Promise.race([execute($, c), $.clock.sleep(window).then(() => PENDING)])
  if (failure === PENDING) {
    if (c.kind === 'fill') abandoned.add(c.id)
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
    if (c.kind === 'fill') {
      failure = await fill($, c)
    } else if (c.kind === 'shell') {
      failure = await shell($, c)
    } else if (slash?.[1].includes('/')) {
      failure = `/${slash[1]} is not a slash command, and Claude takes no prompt starting with /`
    } else if (slash) {
      await $.command.run({ command: slash[1], args: slash[2] })
      if (slash[1] === 'rename') void reportRename($, slash[2])
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

// Resolves with the failure, or '' once filled.
async function fill($: EngineInterface, c: Command): Promise<string> {
  const box = await $.prompt.read()
  const r = await $.prompt.fill({ text: c.text, mode: 'replace' })
  if (!r.isFilled) return refused(r)
  // Acked as accepted while this ran: no Enter follows, so give the draft back.
  if (abandoned.delete(c.id)) {
    await $.prompt.fill({ text: box.text, mode: 'replace' })
    return ''
  }
  filled = { text: c.text, draft: box.text, at: await $.clock.now() }
  return ''
}

function refused(r: { refusal?: string }): string {
  return r.refusal === 'dialog' ? FAIL_BUSY : `the input box refused the text (${r.refusal ?? 'unknown'})`
}

// Runs "!" bash mode input: only a typed "!" turns the mode on. The draft is
// never put back while the Enter may still submit it. Resolves with the
// failure, or '' once submitted.
async function shell($: EngineInterface, c: Command): Promise<string> {
  const { tmuxPane, tmux } = await readEnv($)
  if (tmuxPane === '') return 'bash mode needs a tmux pane'
  const run = (...args: string[]) => tmuxRun($, tmux, args)
  const box = await $.prompt.read()
  const cleared = await $.prompt.fill({ text: '', mode: 'replace' })
  if (!cleared.isFilled) return refused(cleared)
  let entered = false
  try {
    if ((await run('display-message', '-p', '-t', tmuxPane, '#{pane_in_mode}')).trim() === '1') {
      await run('send-keys', '-t', tmuxPane, '-X', 'cancel')
    }
    // Lands in vim INSERT from any state, typing nothing.
    await run('send-keys', '-t', tmuxPane, '--', 'i', 'BSpace')
    await run('send-keys', '-t', tmuxPane, '-l', '--', '!')
    await $.clock.sleep(BANG_MS)
    // Bash mode keeps the "!" out of the box's text.
    if ((await $.prompt.read()).text !== '') return 'bash mode did not turn on'
    const command = c.text.slice(1)
    // A replacing fill would leave bash mode.
    const f = await $.prompt.fill({ text: command, mode: 'insert' })
    if (!f.isFilled) return refused(f)
    await run('send-keys', '-t', tmuxPane, '--', 'Enter')
    entered = true
    if (!(await submitted($, command))) return 'claude did not take the command: the draft was dropped'
    entered = false
    return ''
  } finally {
    if (!entered) await $.prompt.fill({ text: box.text, mode: 'replace' }).catch(() => undefined)
  }
}

async function submitted($: EngineInterface, command: string): Promise<boolean> {
  for (let waited = 0; waited < SUBMIT_WAIT_MS; waited += SUBMIT_POLL_MS) {
    await $.clock.sleep(SUBMIT_POLL_MS)
    if ((await $.prompt.read()).text !== command) return true
  }
  return false
}

// $TMUX starts with the socket of the server that runs Claude's pane.
async function tmuxRun($: EngineInterface, tmux: string, args: string[]): Promise<string> {
  const socket = tmux.split(',')[0]
  const r = await $.process.run(['tmux', ...(socket ? ['-S', socket] : []), ...args])
  if (r.exitCode !== 0) throw new Error(`tmux ${args[0]}: ${r.stderr.trim()}`)
  return r.stdout
}

async function takeDraft($: EngineInterface, text: string): Promise<string | undefined> {
  try {
    const f = filled
    if (!f || f.text.trim() !== text.trim()) return undefined
    filled = undefined
    if (f.draft === '' || (await $.clock.now()) - f.at > DRAFT_TTL_MS) return undefined
    return f.draft
  } catch {
    return undefined
  }
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
