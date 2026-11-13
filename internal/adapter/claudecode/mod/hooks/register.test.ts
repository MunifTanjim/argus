import type { EngineInterface, On } from 'claude-code'
import { expect, mock, test } from 'claude-code/testing'

const OPTIONS = { options: { socket: '/run/argus/mod.sock' } }

type Sent = { path: string; socketPath?: string; body: Record<string, unknown> }

// Answers the mod's requests as argusd would; an Error answer fails the request.
// An answer may be a promise, e.g. one waiting on the mocked clock.
function argusd(on: On, answer: (s: Sent) => unknown): Sent[] {
  const sent: Sent[] = []
  on('http.fetch', async ($, e) => {
    const s = {
      path: new URL(e.url).pathname,
      socketPath: e.init?.socketPath,
      body: JSON.parse(e.init?.body ?? '{}') as Record<string, unknown>,
    }
    sent.push(s)
    const out = await answer(s)
    if (out instanceof Error) throw out
    return { value: { status: 200, ok: true, headers: {}, text: JSON.stringify(out) } }
  })
  return sent
}

// The engine beneath the mod: a held clock, an empty environment and one session.
function world(on: On, env: Record<string, string> = {}) {
  const session = { id: 's1' }
  const clock = mock.clock(on)
  mock.env(on, env)
  on('session.id', () => ({ value: session.id }) as never)
  on('session.cwd', () => ({ value: '/w' }) as never)
  return { clock, session }
}

const ALLOW = JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PermissionRequest',
    decision: { behavior: 'allow', updatedInput: { plan: 'p' } },
  },
})

test('forwards a classic event with the pane and auto mode', OPTIONS, async ($, on) => {
  world(on, { TMUX_PANE: '%3', TMUX: '/tmp/tmux-1000/default,1,0', CLAUDE_CODE_ENABLE_AUTO_MODE: '1' })
  const sent = argusd(on, () => ({}))
  on('classic.Stop', () => ({}))
  await $.classic.Stop({ stop_hook_active: false, session_id: 's1', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never)
  expect(sent.length).toBe(1)
  expect(sent[0].path).toBe('/claude/event')
  expect(sent[0].socketPath).toBe('/run/argus/mod.sock')
  expect(sent[0].body).toEqual(expect.objectContaining({
    event: 'Stop', tmuxPane: '%3', tmux: '/tmp/tmux-1000/default,1,0', autoMode: true,
  }))
  expect((sent[0].body.payload as Record<string, unknown>).session_id).toBe('s1')
})

test('a ran tool call is forwarded without its output', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostToolUse', () => ({}))
  await $.classic.PostToolUse({ session_id: 's1', tool_name: 'Bash', tool_input: { command: 'ls' }, tool_response: { stdout: 'big' } } as never)
  const payload = sent[0].body.payload as Record<string, unknown>
  expect(payload.tool_name).toBe('Bash')
  expect('tool_response' in payload).toBe(false)
})

test('forwards a failed tool call', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostToolUseFailure', () => ({}))
  await $.classic.PostToolUseFailure({ tool_name: 'Bash', tool_input: { command: 'false' }, tool_use_id: 't1', error: 'exit 1' } as never)
  expect(sent.length).toBe(1)
  expect(sent[0].path).toBe('/claude/event')
  expect(sent[0].body.event).toBe('PostToolUseFailure')
  expect((sent[0].body.payload as Record<string, unknown>).tool_name).toBe('Bash')
})

test('an unreachable argusd does not stop the event', OPTIONS, async ($, on) => {
  world(on)
  argusd(on, () => new Error('connect ENOENT'))
  let reached = false
  on('classic.Stop', () => ((reached = true), {}))
  await $.classic.Stop({ stop_hook_active: false } as never)
  expect(reached).toBe(true)
})

test('PreToolUse is sent in the classic shape', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.UserPromptSubmit', () => ({}))
  on('classic.PreToolUse', ($, e) => ({ result: { ok: true } } as never))
  await $.classic.UserPromptSubmit({ prompt: 'hi', transcript_path: '/t/s1.jsonl' } as never)
  await $.tool.call({ tool: 'AskUserQuestion', questions: [] } as never).catch(() => undefined)
  const pre = sent.find(s => s.body.event === 'PreToolUse')
  expect(pre?.body.payload).toEqual(expect.objectContaining({
    hook_event_name: 'PreToolUse',
    tool_name: 'AskUserQuestion',
    tool_input: { questions: [] },
    transcript_path: '/t/s1.jsonl',
  }))
})

test("PreToolUse names the subagent whose call it is", OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PreToolUse', () => ({ result: { ok: true } } as never))
  await $.tool.call({ tool: 'Bash', command: 'ls', agentId: 'a1' }).catch(() => undefined)
  await $.tool.call({ tool: 'Bash', command: 'pwd' }).catch(() => undefined)
  const pre = sent.filter(s => s.body.event === 'PreToolUse').map(s => s.body.payload as Record<string, unknown>)
  expect(pre.length).toBe(2)
  const [sub, main] = pre
  expect(sub).toEqual(expect.objectContaining({ tool_input: { command: 'ls' }, agent_id: 'a1' }))
  expect(main?.tool_input).toEqual({ command: 'pwd' })
  expect(main !== undefined && 'agent_id' in main).toBe(false)
})

// Claude's own dialog: next(e) for classic.PermissionRequest stays pending until
// someone answers it (a local answer abandons the hook instead of resolving).
function dialog(on: On) {
  let answer: (r: object) => void = () => {}
  on('classic.PermissionRequest', () => new Promise(r => (answer = r)) as never)
  return { answer: (r: object) => answer(r) }
}

// argusd's permission endpoints: the event registers request r1, and each
// /claude/decision gets the next answer in line.
function permissionArgusd(on: On, decisions: unknown[]) {
  return argusd(on, s => {
    if (s.path === '/claude/event') return { requestId: 'r1' }
    if (s.path === '/claude/decision') return decisions.shift() ?? new Error('no more answers')
    return {}
  })
}

test("argus's answer wins while Claude's dialog is up", OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  const sent = permissionArgusd(on, [{ done: false }, { done: true, output: ALLOW }])
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
  expect(sent.map(s => s.path)).toEqual(['/claude/event', '/claude/decision', '/claude/decision'])
  expect(sent[1].body).toEqual({ requestId: 'r1' })
  d.answer({}) // let the dialog settle so the test can end
})

test('a cancel decision denies and interrupts', OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  const cancel = JSON.stringify({
    hookSpecificOutput: { hookEventName: 'PermissionRequest', decision: { behavior: 'deny', interrupt: true } },
  })
  permissionArgusd(on, [{ done: true, output: cancel }])
  const res = await $.classic.PermissionRequest({ tool_name: 'AskUserQuestion', tool_input: {} } as never)
  expect(res.decision).toEqual({ behavior: 'deny', interrupt: true })
  d.answer({})
})

test("with no answer from argus, Claude's dialog decides", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const d = dialog(on)
  permissionArgusd(on, [{ done: true, output: '' }])
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  await clock.settle()
  d.answer({ decision: { behavior: 'deny', message: 'from the dialog' } })
  expect((await pending).decision).toEqual({ behavior: 'deny', message: 'from the dialog' })
})

test("Claude's dialog still decides when argusd is unreachable", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const d = dialog(on)
  const slept = sleeps(on, { hold: () => true })
  const sent = argusd(on, () => new Error('connect ENOENT'))
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  await clock.settle()
  d.answer({ decision: { behavior: 'allow' } })
  expect((await pending).decision).toEqual({ behavior: 'allow' })
  // The pause in flight ends with nothing further asked of argus.
  slept.release()
  await clock.settle()
  expect(sent.length).toBe(1)
})

test("a settings hook's decision stands and takes argus's prompt down", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = argusd(on, async s => {
    if (s.path === '/claude/event') return { requestId: 'r1' }
    if (s.path === '/claude/decision') return clock.sleep(1000).then(() => ({ done: false }))
    return {}
  })
  on('classic.PermissionRequest', async () => {
    await clock.sleep(10) // decides after argus registered the request
    return { decision: { behavior: 'deny', message: 'no' } }
  })
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  await clock.settle()
  await clock.advance(10)
  const res = await pending
  expect(res.decision).toEqual({ behavior: 'deny', message: 'no' })
  expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
  // The wait in flight ends with nothing further asked of argus.
  await clock.advance(1000)
  expect(sent.filter(s => s.path === '/claude/decision').length).toBe(1)
})

test('the poll runs prompts and slash commands', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [
    {
      commands: [
        { id: '1', kind: 'prompt', text: 'fix the bug' },
        { id: '2', kind: 'prompt', text: '/compact keep tests' },
        { id: '3', kind: 'prompt', text: '/tmp/app.log shows a crash' },
        { id: '4', kind: 'prompt', text: '/memory\nmore' },
      ],
    },
  ]
  // Once drained, fail the poll so the loop backs off on the held clock.
  const polls = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  const prompts: unknown[] = []
  const commands: unknown[] = []
  on('prompt.submit', ($, e) => (prompts.push({ text: e.text }), { text: e.text }) as never)
  on('command.run', ($, e) => (commands.push({ command: e.command, args: e.args }), { text: '' } as never))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(prompts).toEqual([{ text: 'fix the bug' }])
  expect(commands).toEqual([{ command: 'compact', args: 'keep tests' }, { command: 'memory', args: 'more' }])
  expect(polls.filter(s => s.path === '/claude/poll').map(s => s.body.sessionId)).toEqual(['s1', 's1'])
  const acks = polls.filter(s => s.path === '/claude/ack').map(s => s.body)
  expect(acks.map(a => a.id)).toEqual(['1', '2', '3', '4'])
  expect(acks[0]).toEqual({ id: '1', ran: true })
  expect(acks[1]).toEqual({ id: '2', ran: true })
  expect(String(acks[2].error)).toContain('/tmp/app.log is not a slash command')
})

test('poll sends the kinds it runs', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = argusd(on, () => new Error('hold'))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  const poll = sent.find(s => s.path === '/claude/poll')
  expect(poll?.body.kinds).toEqual(['prompt', 'exit', 'fill', 'shell', 'commands'])
})

test('commands kind acks the command list', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [{ commands: [{ id: '7', kind: 'commands', text: '' }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('command.list', () => ({ value: [{ name: 'compact', description: 'Free up context', source: 'builtin' }] }) as never)
  const submitted: unknown[] = []
  on('prompt.submit', ($, e) => (submitted.push(e.text), { text: e.text }) as never)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(sent.filter(s => s.path === '/claude/ack').map(s => s.body)).toEqual([
    { id: '7', ran: true, data: [{ name: 'compact', description: 'Free up context' }] },
  ])
  expect(submitted).toEqual([])
})

test('a failed command list is acked with its error', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [{ commands: [{ id: '7', kind: 'commands', text: '' }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('command.list', () => { throw new Error('no list') })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  const acks = sent.filter(s => s.path === '/claude/ack').map(s => s.body)
  expect(acks.length).toBe(1)
  expect(acks[0].id).toBe('7')
  expect(String(acks[0].error)).toContain('command.list')
  expect(acks[0].ran).toBeUndefined()
})

test('a failed or dropped command is acked with its error', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [
    { commands: [{ id: '1', kind: 'prompt', text: 'hi' }, { id: '2', kind: 'prompt', text: '/nope' }] },
  ]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('prompt.submit', () => ({ drop: 'blocked by policy' }) as never)
  // A failing hook is skipped, so the mod sees command.run fail with nothing beneath.
  on('command.run', () => { throw new Error('unknown command') })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  const acks = sent.filter(s => s.path === '/claude/ack').map(s => s.body)
  expect(acks.length).toBe(2)
  expect(acks[0]).toEqual({ id: '1', error: 'prompt dropped: blocked by policy' })
  expect(acks[1].id).toBe('2')
  expect(String(acks[1].error)).toContain('command.run')
})

// A prompt typed over a running turn, or a command whose panel stays open,
// resolves late: it is acked as accepted and the poll goes on.
test('a pending command is acked after the window and the poll goes on', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [{ commands: [{ id: '1', kind: 'prompt', text: '/cost' }] }, { commands: [] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('command.run', () => new Promise(() => {}) as never)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(sent.filter(s => s.path === '/claude/ack').length).toBe(0)
  await clock.advance(1000)
  expect(sent.filter(s => s.path === '/claude/ack').map(s => s.body)).toEqual([{ id: '1' }])
  expect(sent.filter(s => s.path === '/claude/poll').length).toBeGreaterThan(1)
})

// A rename reaches argusd once it ran, wherever it came from; a bare or failed
// one does not.
test('a rename is forwarded once it ran', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('command.run', ($, e) => {
    if (e.args === 'Broken') throw new Error('rename failed')
    return { text: '' } as never
  })
  await $.command.run({ command: 'rename', args: '  New name ' })
  await $.command.run({ command: 'rename', args: '' })
  await $.command.run({ command: 'rename', args: 'Broken' }).catch(() => undefined)
  await $.command.run({ command: 'compact', args: 'x' })
  const events = sent.filter(s => s.path === '/claude/event').map(s => s.body)
  expect(events.length).toBe(1)
  expect(events[0].event).toBe('SessionRename')
  expect(events[0].payload).toEqual(expect.objectContaining({ session_id: 's1', name: 'New name' }))
})

// A /rename argus sends runs through the mod's own $.command.run, which its own
// command.run hook does not see: the run itself reports it, once.
test('a rename argus sends is forwarded once it ran', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [{ commands: [{ id: '1', kind: 'prompt', text: '/rename New name' }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('command.run', () => ({ text: '' }) as never)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  const events = sent.filter(s => s.path === '/claude/event').map(s => s.body)
  expect(events.map(e => e.event)).toEqual(['SessionRename'])
  expect(events[0].payload).toEqual(expect.objectContaining({ session_id: 's1', name: 'New name' }))
})

test('the poll backs off while argusd is down', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  let up = false
  let served = false
  // Down, then up for exactly one poll (later polls fail so the held clock stops).
  const sent = argusd(on, () => {
    if (!up) return new Error('connect ENOENT')
    if (served) return new Error('hold')
    served = true
    return { commands: [] }
  })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(sent.length).toBe(1)
  await clock.advance(999)
  expect(sent.length).toBe(1)
  await clock.advance(1)
  expect(sent.length).toBe(2)
  await clock.advance(2000)
  expect(sent.length).toBe(3)
  // argusd is back: the retry due in 4 s succeeds and the next poll goes out at once.
  up = true
  await clock.advance(4000)
  expect(sent.filter(s => s.path === '/claude/poll').length).toBe(5)
})

test('each poll reads the session id afresh', OPTIONS, async ($, on) => {
  const { clock, session } = world(on)
  let polls = 0
  const sent = argusd(on, () => {
    polls++
    if (polls === 1) session.id = 's2' // /clear: the process goes on under a new id
    return polls <= 2 ? { commands: [] } : new Error('hold')
  })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(sent.map(s => s.body.sessionId)).toEqual(['s1', 's2', 's2'])
})

test('a SessionStart restarts the poll under the new session id at once', OPTIONS, async ($, on) => {
  const { clock, session } = world(on)
  // argusd holds every poll for 25 s.
  const sent = argusd(on, async s => {
    if (s.path !== '/claude/poll') return {}
    await clock.sleep(25000)
    return { commands: [] }
  })
  const pollIds = () => sent.filter(s => s.path === '/claude/poll').map(s => s.body.sessionId)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('classic.SessionStart', () => ({}))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(pollIds()).toEqual(['s1'])
  session.id = 's2' // /clear
  await $.classic.SessionStart({ source: 'clear', session_id: 's2', cwd: '/w' } as never)
  await clock.settle()
  expect(pollIds()).toEqual(['s1', 's2'])
  // The old poll returns and its loop stops; only the new loop polls again.
  await clock.advance(25000)
  expect(pollIds()).toEqual(['s1', 's2', 's2'])
  await clock.advance(25000)
  expect(pollIds()).toEqual(['s1', 's2', 's2', 's2'])
})

test('with no socket, a classic event asks nothing of argusd', { options: {} }, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  let reached = false
  on('classic.Stop', () => ((reached = true), {}))
  await $.classic.Stop({ stop_hook_active: false, session_id: 's1' } as never)
  expect(reached).toBe(true)
  expect(sent.length).toBe(0)
})

test("with no socket, Claude's dialog decides and argusd is never asked", { options: {} }, async ($, on) => {
  const { clock } = world(on)
  const d = dialog(on)
  const sent = permissionArgusd(on, [{ done: true, output: ALLOW }])
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  await clock.settle()
  d.answer({ decision: { behavior: 'deny', message: 'from the dialog' } })
  expect((await pending).decision).toEqual({ behavior: 'deny', message: 'from the dialog' })
  expect(sent.length).toBe(0)
})

test("a settings hook that doesn't decide still waits for argus", OPTIONS, async ($, on) => {
  world(on)
  on('classic.PermissionRequest', () => ({}) as never)
  permissionArgusd(on, [{ done: false }, { done: true, output: ALLOW }])
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
})

test('no decision from either side returns the settings result', OPTIONS, async ($, on) => {
  world(on)
  on('classic.PermissionRequest', () => ({}) as never)
  permissionArgusd(on, [{ done: true, output: '' }])
  const res = await $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  expect(res.decision).toBe(undefined)
})

const TURN = { answer: 'done', durationMs: 1, isAborted: false, turnId: 't1', reason: 'answer' as const }

test('a main-loop turn end is reported as a Stop', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('turn.complete', ($, e) => ({ text: e.answer }) as never)
  await $.turn.complete(TURN)
  const stops = sent.filter(s => s.body.event === 'Stop')
  expect(stops.length).toBe(1)
  expect(stops[0].path).toBe('/claude/event')
  expect(stops[0].body.payload).toEqual(expect.objectContaining({
    hook_event_name: 'Stop', session_id: 's1', cwd: '/w', stop_hook_active: false,
  }))
})

test("a subagent's turn end is not reported", OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('turn.complete', ($, e) => ({ text: e.answer }) as never)
  await $.turn.complete({ ...TURN, agentId: 'a1' })
  expect(sent.length).toBe(0)
})

type Listed = { id: string; type: string; description: string; status: string; teammateId?: string; parentId?: string }

function subagentEvents(sent: Sent[]) {
  return sent.filter(s => s.body.event === 'Subagents').map(s => s.body.payload as Record<string, unknown>)
}

const START = { agent_id: 'a1', agent_type: 'Explore', session_id: 's1', transcript_path: '/t/s1.jsonl', cwd: '/w' }

test('reports running subagents when one starts', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SubagentStart', () => ({}))
  await $.classic.SubagentStart(START as never)
  const events = subagentEvents(sent)
  expect(events.length).toBe(1)
  expect(events[0]).toEqual(expect.objectContaining({ hook_event_name: 'Subagents', session_id: 's1', cwd: '/w' }))
  expect(events[0].subagents).toEqual([{ id: 'a1', type: 'Explore', desc: 'map code' }])
})

test('a starting subagent not yet listed is still reported', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SubagentStart', () => ({}))
  await $.classic.SubagentStart(START as never)
  expect(subagentEvents(sent)[0].subagents).toEqual([{ id: 'a1', type: 'Explore', desc: '' }])
})

test('a stopping subagent is left out even while still listed', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SubagentStop', () => ({}))
  await $.classic.SubagentStop({ agent_id: 'a1', agent_type: 'Explore', session_id: 's1' } as never)
  expect(subagentEvents(sent)[0].subagents).toEqual([])
})

test('only running top-level subagents are reported', OPTIONS, async ($, on) => {
  world(on)
  const l = (id: string, status: string, extra: Partial<Listed> = {}): Listed => ({ id, type: 'Explore', description: id, status, ...extra })
  on('agent.list', () => ({
    value: [
      l('c', 'completed'), l('k', 'killed'), l('i', 'idle'),
      l('t', 'running', { teammateId: 'tm' }), l('n', 'running', { parentId: 'p' }),
      l('p1', 'pending'), l('r', 'running'), l('w', 'waiting'),
    ],
  }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SubagentStart', () => ({}))
  await $.classic.SubagentStart({ ...START, agent_id: 'r' } as never)
  expect((subagentEvents(sent)[0].subagents as { id: string }[]).map(a => a.id)).toEqual(['p1', 'r', 'w'])
})

test('session start re-sends the snapshot', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  expect(subagentEvents(sent).length).toBe(1)
})

test('every main turn end re-sends the snapshot', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('turn.complete', ($, e) => ({ text: e.answer }) as never)
  await $.turn.complete(TURN)
  await $.turn.complete(TURN)
  expect(subagentEvents(sent).length).toBe(2)
})

// A permission request with no settings decision, argusd holding its decision
// for 25 s, then a tool call that Claude answers with a deny.
function heldRequest(on: On, clock: ReturnType<typeof world>['clock']) {
  on('classic.PermissionRequest', () => ({}) as never)
  return argusd(on, async s => {
    if (s.path === '/claude/event') return { requestId: 'r1' }
    if (s.path === '/claude/decision') return clock.sleep(25000).then(() => ({ done: true, output: '' }))
    return {}
  })
}

test("a local deny with feedback ends argus's request", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  on('tool.call', () => ({ result: 'denied', isError: true }) as never)
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await $.tool.call({ tool: 'Bash', command: 'touch x' } as never).catch(() => undefined)
  expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
  await clock.advance(25000)
  const res = await pending
  expect(res.decision).toBe(undefined)
  const paths = sent.map(s => s.path)
  expect(paths.slice(paths.indexOf('/claude/cancel') + 1)).not.toContain('/claude/decision')
})

test("a tool call for another command leaves argus's request open", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  on('tool.call', () => ({ result: 'ok' }) as never)
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await $.tool.call({ tool: 'Bash', command: 'ls' } as never).catch(() => undefined)
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  await clock.advance(25000) // the held decision returns, ending the request
  await pending
})

// The retry pauses: each `sleep` the mod runs resolves at once, or with
// advance, moves the clock on by its length first. hold, while it returns
// true, keeps a pause pending until release.
function sleeps(on: On, opts: { advance?: ReturnType<typeof world>['clock']; hold?: () => boolean } = {}) {
  const ran: string[] = []
  let release: () => void = () => {}
  const held = new Promise<void>(r => (release = r))
  on('process.run', async ($, e) => {
    const [cmd, arg] = e.argv
    if (cmd !== 'sleep') throw new Error(`unexpected process ${cmd}`)
    ran.push(arg)
    if (opts.advance) await opts.advance.advance(Number(arg) * 1000)
    if (opts.hold?.()) await held
    return { value: { exitCode: 0, stdout: '', stderr: '' } } as never
  })
  return { ran, release: () => release() }
}

// argusd across restarts: each /claude/event and /claude/decision gets the
// next answer in its line.
function restartingArgusd(on: On, events: unknown[], decisions: unknown[]) {
  return argusd(on, s => {
    if (s.path === '/claude/event') return events.shift() ?? new Error('no more registrations')
    if (s.path === '/claude/decision') return decisions.shift() ?? new Error('no more answers')
    return {}
  })
}

// A plugin above the mod settles first, aborting the hooks beneath, as Esc on
// Claude's dialog does.
const ABORTED = {
  ...OPTIONS,
  plugins: [{
    name: 'above',
    tier: 'prepend' as const,
    register(on: On) {
      on('classic.PermissionRequest', async ($, e, next) => {
        next(e).catch(() => undefined)
        await $.clock.sleep(10)
        return {}
      })
    },
  }],
}

test('an aborted hook cancels the request in argusd', ABORTED, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await clock.advance(10)
  await pending
  await clock.settle()
  expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
})

test('a hook aborted while registering cancels the request once registered', ABORTED, async ($, on) => {
  const { clock } = world(on)
  on('classic.PermissionRequest', () => ({}) as never)
  let register: (r: object) => void = () => {}
  const sent = argusd(on, s => {
    if (s.path === '/claude/event') return new Promise(r => (register = r))
    return { done: false }
  })
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await clock.advance(10)
  await pending
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  register({ requestId: 'r1' })
  await clock.settle()
  expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
  expect(sent.some(s => s.path === '/claude/decision')).toBe(false)
})

test("a subagent's tool call does not end the main thread's request", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  on('tool.call', () => ({ result: 'ok' }) as never)
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await $.tool.call({ tool: 'Bash', command: 'touch x', agentId: 'a1' } as never).catch(() => undefined)
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  await clock.advance(25000)
  await pending
})

test("a tool call from another agent loop does not end a subagent's request", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  on('tool.call', () => ({ result: 'ok' }) as never)
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' }, agent_id: 'a1' } as never)
  await clock.settle()
  await $.tool.call({ tool: 'Bash', command: 'touch x', agentId: 'a2' } as never).catch(() => undefined)
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  await $.tool.call({ tool: 'Bash', command: 'touch x', agentId: 'a1' } as never).catch(() => undefined)
  expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
  await clock.advance(25000)
  await pending
})

// A main-thread Bash call whose permission step raises the request and waits
// until argus's held decision returns. Rows draw as the engine would.
function askingCall($: EngineInterface, on: On) {
  drawn(on)
  on('tool.call', async (_$, e) => {
    const { tool, tool_use_id: _, agentId, ...input } = e as Record<string, unknown>
    await $.classic.PermissionRequest({
      tool_name: tool, tool_input: input, ...(agentId ? { agent_id: agentId } : {}),
    } as never)
    return { result: 'ok' }
  })
}

// The engine's drawing of every row; its part names the test, not the mod.
const ROW = { type: 'Text', props: {}, children: ['row'] }
function drawn(on: On) {
  on('ui.render', () => ROW as never)
}

function toolGroup(id: string, isRunning: boolean) {
  return {
    surface: 'terminal' as const,
    component: 'ToolGroup' as const,
    requestId: 'g1',
    props: {
      calls: [{ tool_use_id: id, tool: 'Bash', input: { command: 'touch x' }, isRunning, isErrored: false, isInterrupted: false }],
      isActive: true,
      isExpanded: false,
      isCallsOnly: false,
    },
  }
}

function toolUse(id: string, isRunning: boolean) {
  return {
    surface: 'terminal' as const,
    component: 'ToolUse' as const,
    requestId: id,
    props: { tool_use_id: id, tool: 'Bash', input: { command: 'touch x' }, isRunning, isErrored: false, isInterrupted: false },
  }
}

for (const [name, row] of [['grouped', toolGroup], ['expanded', toolUse]] as const) {
  test(`a local allow ends the request when its ${name} tool row starts running`, OPTIONS, async ($, on) => {
    const { clock } = world(on)
    const sent = heldRequest(on, clock)
    askingCall($, on)
    const call = $.tool.call({ tool: 'Bash', command: 'touch x', tool_use_id: 'tu1' } as never)
    await clock.settle()
    await $.ui.render(row('tu1', false) as never)
    await clock.settle()
    expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
    await $.ui.render(row('tu1', true) as never)
    await clock.settle()
    expect(sent.find(s => s.path === '/claude/cancel')?.body).toEqual({ requestId: 'r1' })
    await clock.advance(25000)
    await call
    expect(sent.filter(s => s.path === '/claude/cancel')).toHaveLength(1)
  })
}

test("another call's running row does not end the request", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  askingCall($, on)
  const call = $.tool.call({ tool: 'Bash', command: 'touch x', tool_use_id: 'tu1' } as never)
  await clock.settle()
  await $.ui.render(toolGroup('tu2', true) as never)
  await $.ui.render(toolUse('tu2', true) as never)
  await clock.settle()
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  await clock.advance(25000)
  await call
})

test("a subagent's call does not link to the main thread's ask", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const sent = heldRequest(on, clock)
  drawn(on)
  let release: () => void = () => {}
  on('tool.call', () => new Promise(r => (release = () => r({ result: 'ok' }))) as never)
  const sub = $.tool.call({ tool: 'Bash', command: 'touch x', tool_use_id: 'tu2', agentId: 'a1' } as never)
  await clock.settle()
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: { command: 'touch x' } } as never)
  await clock.settle()
  await $.ui.render(toolGroup('tu2', true) as never)
  await $.ui.render(toolUse('tu2', true) as never)
  await clock.settle()
  expect(sent.some(s => s.path === '/claude/cancel')).toBe(false)
  await clock.advance(25000)
  await pending
  release()
  await sub
})

test('the render passes through unchanged', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  heldRequest(on, clock)
  askingCall($, on)
  const call = $.tool.call({ tool: 'Bash', command: 'touch x', tool_use_id: 'tu1' } as never)
  await clock.settle()
  const tree = { ...ROW, part: { plugin: 'test' } }
  expect(await $.ui.render(toolGroup('tu1', true) as never)).toEqual(tree)
  expect(await $.ui.render(toolUse('tu1', true) as never)).toEqual(tree)
  await clock.advance(25000)
  await call
})

test('the wait survives argusd going away', OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  const slept = sleeps(on)
  restartingArgusd(on, [{ requestId: 'r1', instanceId: 'n1' }], [
    new Error('connect ENOENT'),
    new Error('connect ENOENT'),
    { done: true, output: ALLOW, instanceId: 'n1' },
  ])
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
  expect(slept.ran).toEqual(['1', '1', '1'])
  d.answer({})
})

test('a settled request stops pausing within a step', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const d = dialog(on)
  const slept = sleeps(on, { advance: clock, hold: () => clock.now() >= 10000 })
  const sent = argusd(on, () => new Error('connect ENOENT'))
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  while (clock.now() < 10000) await clock.settle()
  d.answer({ decision: { behavior: 'allow' } })
  expect((await pending).decision).toEqual({ behavior: 'allow' })
  const asked = sent.length
  slept.release()
  await clock.settle()
  // Pauses of 1, 2, 4 and 5 s: the third step of the 5 s pause is the last.
  expect(slept.ran).toEqual(Array(10).fill('1'))
  expect(sent.length).toBe(asked)
})

test('a restarted argusd gets the request again', OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  sleeps(on)
  const sent = restartingArgusd(on,
    [{ requestId: 'r1', instanceId: 'n1' }, { requestId: 'r2', instanceId: 'n2' }],
    [new Error('connect ENOENT'), { done: true, instanceId: 'n2' }, { done: true, output: ALLOW, instanceId: 'n2' }],
  )
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
  expect(sent.filter(s => s.path === '/claude/event').length).toBe(2)
  expect(sent.filter(s => s.path === '/claude/decision').at(-1)?.body).toEqual({ requestId: 'r2' })
  d.answer({})
})

test('a second restart registers again', OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  sleeps(on)
  const sent = restartingArgusd(on,
    [{ requestId: 'r1', instanceId: 'n1' }, { requestId: 'r2', instanceId: 'n2' }, { requestId: 'r3', instanceId: 'n3' }],
    [
      new Error('connect ENOENT'),
      { done: true, instanceId: 'n2' },
      new Error('connect ENOENT'),
      { done: true, instanceId: 'n3' },
      { done: false, instanceId: 'n3' },
      { done: true, output: ALLOW, instanceId: 'n3' },
    ],
  )
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
  const paths = sent.map(s => s.path)
  expect(paths.filter(p => p === '/claude/event').length).toBe(3)
  const last = sent.slice(paths.lastIndexOf('/claude/event') + 1).filter(s => s.path === '/claude/decision')
  expect(last.map(s => s.body)).toEqual([{ requestId: 'r3' }, { requestId: 'r3' }])
  d.answer({})
})

test('a reply with no instance id does not register again', OPTIONS, async ($, on) => {
  world(on)
  const d = dialog(on)
  const sent = permissionArgusd(on, [{ done: false }, { done: true, output: ALLOW }])
  const res = await $.classic.PermissionRequest({ tool_name: 'ExitPlanMode', tool_input: { plan: 'p' } } as never)
  expect(res.decision).toEqual({ behavior: 'allow', updatedInput: { plan: 'p' } })
  expect(sent.filter(s => s.path === '/claude/event').length).toBe(1)
  d.answer({})
})

test("a subagent's wait gives up after 90 s of outage", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  on('classic.PermissionRequest', () => ({}) as never)
  sleeps(on, { advance: clock })
  argusd(on, () => new Error('connect ENOENT'))
  const res = await $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {}, agent_id: 'a1' } as never)
  expect(res.decision).toBe(undefined)
  expect(clock.now()).toBeGreaterThanOrEqual(90000)
  expect(clock.now()).toBeLessThan(95000)
})

test("the main thread's wait does not give up", OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const d = dialog(on)
  const slept = sleeps(on, { advance: clock, hold: () => clock.now() >= 300000 })
  argusd(on, () => new Error('connect ENOENT'))
  let done = false
  const pending = $.classic.PermissionRequest({ tool_name: 'Bash', tool_input: {} } as never)
  void pending.then(() => (done = true))
  while (clock.now() < 300000) await clock.settle()
  await clock.settle()
  expect(done).toBe(false)
  d.answer({ decision: { behavior: 'allow' } })
  expect((await pending).decision).toEqual({ behavior: 'allow' })
  slept.release()
})

// Kill must not wait out the running turn, and the ack must leave before Claude exits.
test('exit ends the running turn, acks, then exits', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const queue = [{ commands: [{ id: '1', kind: 'exit', text: '' }] }]
  const order: string[] = []
  argusd(on, s => {
    if (s.path === '/claude/poll') return queue.shift() ?? new Error('hold')
    if (s.path === '/claude/ack') order.push('ack')
    return {}
  })
  on('turn.start', ($, e) => ({ turnId: e.turnId }))
  on('turn.abort', ($, e) => (order.push('abort ' + e.turnId), { value: undefined }) as never)
  on('command.run', ($, e) => (order.push('run ' + e.command), { text: '' }) as never)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.turn.start({ text: 'work', turnId: 't1' } as never)
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(order).toEqual(['abort t1', 'ack', 'run exit'])
})

const STEP = { turnId: 't1', index: 0, model: 'claude-opus-5-5', effort: 'high' as const, messageCount: 1 }

// The engine answering model requests beneath the hooks; registered once per test.
function engineSteps(on: On) {
  on('turn.step', async function* (_, e) { return { turnId: e.turnId, index: e.index, answer: '', toolUses: [] } as never })
}

// Runs one model request through the hooks.
async function step($: EngineInterface, e: Record<string, unknown>) {
  const s = $.turn.step(e as never)
  for await (const _ of s) { /* drain */ }
}

function modelInfos(sent: Sent[]) {
  return sent.filter(s => s.body.event === 'ModelInfo').map(s => s.body.payload as Record<string, unknown>)
}

test('a session start reports the model', OPTIONS, async ($, on) => {
  world(on)
  on('session.model', () => ({ value: 'claude-opus-5-5[1m]' }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SessionStart', () => ({}))
  await $.classic.SessionStart({ session_id: 's1', source: 'startup', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never)
  const infos = modelInfos(sent)
  expect(infos.length).toBe(1)
  expect(infos[0]).toEqual(expect.objectContaining({ hook_event_name: 'ModelInfo', session_id: 's1', model: 'claude-opus-5-5[1m]' }))
  expect('effort' in infos[0]).toBe(false)
})

test('a model switch reports the new model', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostModelSwitch', () => ({}))
  await $.classic.PostModelSwitch({ session_id: 's1', from_model: 'claude-opus-5-5', to_model: 'claude-sonnet-5-5' } as never)
  expect(modelInfos(sent).map(p => p.model)).toEqual(['claude-sonnet-5-5'])
})

test('main-loop steps report model and effort on change only', OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  const sent = argusd(on, () => ({}))
  await step($, STEP)
  await step($, { ...STEP, index: 1 })
  await step($, { ...STEP, index: 2, effort: undefined })
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([
    ['claude-opus-5-5', 'high'],
    ['claude-opus-5-5', ''],
  ])
})

test("a subagent's step is not reported", OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  const sent = argusd(on, () => ({}))
  await step($, { ...STEP, agentId: 'a1' })
  expect(modelInfos(sent).length).toBe(0)
})

test('a new session start reports the effort again', OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  on('session.model', () => ({ value: 'claude-opus-5-5' }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SessionStart', () => ({}))
  const start = { session_id: 's1', source: 'startup', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never
  await step($, STEP)
  await $.classic.SessionStart(start)
  await step($, STEP)
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([
    ['claude-opus-5-5', 'high'],
    ['claude-opus-5-5', undefined],
    ['claude-opus-5-5', 'high'],
  ])
})

// argusd keeps live models in memory only, so after it was unreachable the
// session reports its model and effort again.
test('a recovered poll reports the model and effort again', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  engineSteps(on)
  on('session.model', () => ({ value: 'claude-opus-5-5' }) as never)
  let polls = 0
  const sent = argusd(on, s => {
    if (s.path !== '/claude/poll') return {}
    polls++
    return polls === 1 ? new Error('connect ENOENT') : polls === 2 ? { commands: [] } : new Error('hold')
  })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  await step($, STEP)
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([['claude-opus-5-5', 'high']])
  await clock.advance(1000)
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([
    ['claude-opus-5-5', 'high'],
    ['claude-opus-5-5', undefined],
  ])
  await step($, STEP)
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([
    ['claude-opus-5-5', 'high'],
    ['claude-opus-5-5', undefined],
    ['claude-opus-5-5', 'high'],
  ])
})

test('classic session start re-sends the subagents', OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  on('classic.SessionStart', () => ({}))
  await $.classic.SessionStart({ session_id: 's1', source: 'clear', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never)
  expect(subagentEvents(sent).map(e => e.subagents)).toEqual([[{ id: 'a1', type: 'Explore', desc: 'map code' }]])
})

test('a recovered poll re-sends the subagents', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  engineSteps(on)
  on('agent.list', () => ({ value: [{ id: 'a1', type: 'Explore', description: 'map code', status: 'running' }] as Listed[] }) as never)
  let polls = 0
  const sent = argusd(on, s => {
    if (s.path !== '/claude/poll') return {}
    polls++
    return polls === 1 ? new Error('connect ENOENT') : polls === 2 ? { commands: [] } : new Error('hold')
  })
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  const before = subagentEvents(sent).length
  await clock.advance(1000)
  expect(subagentEvents(sent).length).toBe(before + 1)
})

test('a numeric effort reports no label', OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  const sent = argusd(on, () => ({}))
  await step($, { ...STEP, effort: 31999 })
  expect(modelInfos(sent).map(p => [p.model, p.effort])).toEqual([['claude-opus-5-5', '']])
})

function inputBox(on: On, draft: string, refusal?: 'dialog') {
  const fills: string[] = []
  on('prompt.read', () => ({ value: { text: draft, cursor: draft.length } }) as never)
  on('prompt.fill', ($, e) => {
    if (refusal) return { isFilled: false, refusal } as never
    fills.push(e.text)
    return { isFilled: true } as never
  })
  return fills
}

async function runFill(
  $: EngineInterface,
  on: On,
  clock: ReturnType<typeof mock.clock>,
  text: string,
) {
  const queue = [{ commands: [{ id: '1', kind: 'fill', text }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('prompt.submit', ($, e) => ({ text: e.text }) as never)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  return sent.filter(s => s.path === '/claude/ack').map(s => s.body)
}

test('a fill replaces the draft and puts it back once submitted', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const fills = inputBox(on, 'my draft')
  const acks = await runFill($, on, clock, 'steer')
  expect(acks).toEqual([{ id: '1', ran: true }])
  expect(fills).toEqual(['steer'])
  await $.prompt.submit({ text: 'steer' } as never) // argus's Enter
  expect(fills).toEqual(['steer', 'my draft'])
})

test('a submit with whitespace trimmed still puts the draft back', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const fills = inputBox(on, 'my draft')
  await runFill($, on, clock, 'steer\n')
  await $.prompt.submit({ text: 'steer' } as never)
  expect(fills).toEqual(['steer\n', 'my draft'])
})

test('with no draft nothing is written back', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const fills = inputBox(on, '')
  await runFill($, on, clock, 'steer')
  await $.prompt.submit({ text: 'steer' } as never)
  expect(fills).toEqual(['steer'])
})

test('a draft is not put back after its fill expired', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const fills = inputBox(on, 'my draft')
  await runFill($, on, clock, 'steer')
  await clock.advance(6000)
  await $.prompt.submit({ text: 'steer' } as never)
  expect(fills).toEqual(['steer'])
})

// The harness strips a refusal cause a test hook writes (only the engine's own
// survives), so the 'dialog' cause and its 'busy' ack cannot be reached here.
test('a refused fill fails without the busy ack', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  inputBox(on, 'my draft', 'dialog')
  const acks = await runFill($, on, clock, 'steer')
  expect(acks).toEqual([{ id: '1', error: 'the input box refused the text (unknown)' }])
})

test('a fill that lands after its accepted ack gives the draft back', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const fills: string[] = []
  on('prompt.read', () => ({ value: { text: 'my draft', cursor: 8 } }) as never)
  on('prompt.fill', async ($, e) => {
    if (fills.length === 0) await clock.sleep(1500) // past the ack window
    fills.push(e.text)
    return { isFilled: true } as never
  })
  const queue = [{ commands: [{ id: '1', kind: 'fill', text: 'steer' }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  await clock.settle()
  await clock.advance(1500)
  await clock.settle()
  expect(sent.filter(s => s.path === '/claude/ack').map(s => s.body)).toEqual([{ id: '1' }])
  expect(fills).toEqual(['steer', 'my draft'])
})

// Claude's input box under tmux keys: a typed "!" on an empty box turns bash
// mode on, Enter submits, and a replacing fill leaves bash mode.
function bashBox(on: On, draft: string, opts: { bang?: boolean; submits?: boolean } = {}) {
  const box = { text: draft, bash: false }
  const keys: string[][] = []
  const fills: { text: string; mode: string }[] = []
  const ran: string[] = []
  on('prompt.read', () => ({ value: { text: box.text, cursor: box.text.length } }) as never)
  on('prompt.fill', ($, e) => {
    fills.push({ text: e.text, mode: e.mode })
    if (e.mode === 'replace') box.bash = false
    box.text = e.mode === 'replace' ? e.text : box.text + e.text
    return { isFilled: true } as never
  })
  on('process.run', ($, e) => {
    const argv = [...e.argv]
    const sub = argv.indexOf('send-keys') >= 0 ? argv.slice(argv.indexOf('send-keys') + 1) : []
    if (argv.includes('display-message')) return { value: { exitCode: 0, stdout: '0\n', stderr: '' } } as never
    keys.push(argv)
    const typed = sub.slice(sub.indexOf('--') + 1)
    if (sub.includes('-l') && typed[0] === '!' && box.text === '') {
      if (opts.bang === false) box.text = '!'
      else box.bash = true
    }
    if (typed[0] === 'Enter' && opts.submits !== false) {
      ran.push(`${box.bash ? 'bash' : 'prompt'}:${box.text}`)
      box.text = ''
      box.bash = false
    }
    return { value: { exitCode: 0, stdout: '', stderr: '' } } as never
  })
  return { box, keys, fills, ran }
}

async function runShell($: EngineInterface, on: On, clock: ReturnType<typeof mock.clock>, text: string) {
  const queue = [{ commands: [{ id: '1', kind: 'shell', text }] }]
  const sent = argusd(on, s => (s.path === '/claude/poll' ? queue.shift() ?? new Error('hold') : {}))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  await $.session.start({ cwd: '/w', surface: 'terminal', isInteractive: true })
  for (let i = 0; i < 60; i++) {
    await clock.settle()
    await clock.advance(50)
  }
  return sent.filter(s => s.path === '/claude/ack').map(s => s.body)
}

const TMUX_ENV = { TMUX_PANE: '%3', TMUX: '/tmp/tmux-1000/argus,1,0' }

test('a shell command runs in bash mode and the draft comes back', OPTIONS, async ($, on) => {
  const { clock } = world(on, TMUX_ENV)
  const b = bashBox(on, 'my draft')
  const acks = await runShell($, on, clock, '! ls -la')
  expect(acks).toEqual([{ id: '1', ran: true }])
  expect(b.ran).toEqual(['bash: ls -la'])
  expect(b.box).toEqual({ text: 'my draft', bash: false })
  expect(b.fills).toEqual([{ text: '', mode: 'replace' }, { text: ' ls -la', mode: 'insert' }, { text: 'my draft', mode: 'replace' }])
  expect(b.keys.every(k => k[0] === 'tmux' && k[1] === '-S' && k[2] === '/tmp/tmux-1000/argus' && k.includes('%3'))).toBe(true)
})

test('a multi-line shell command runs as one bash mode command', OPTIONS, async ($, on) => {
  const { clock } = world(on, TMUX_ENV)
  const b = bashBox(on, '')
  const acks = await runShell($, on, clock, '! echo a\necho b')
  expect(acks).toEqual([{ id: '1', ran: true }])
  expect(b.ran).toEqual(['bash: echo a\necho b'])
})

test('a shell command needs a tmux pane', OPTIONS, async ($, on) => {
  const { clock } = world(on)
  const b = bashBox(on, 'my draft')
  const acks = await runShell($, on, clock, '! ls')
  expect(acks).toEqual([{ id: '1', error: 'bash mode needs a tmux pane' }])
  expect(b.keys).toEqual([])
  expect(b.box.text).toBe('my draft')
})

test('a "!" that does not turn bash mode on runs nothing', OPTIONS, async ($, on) => {
  const { clock } = world(on, TMUX_ENV)
  const b = bashBox(on, 'my draft', { bang: false })
  const acks = await runShell($, on, clock, '! ls')
  expect(acks).toEqual([{ id: '1', error: 'bash mode did not turn on' }])
  expect(b.ran).toEqual([])
  expect(b.box).toEqual({ text: 'my draft', bash: false })
})

test('an Enter Claude never takes keeps the draft out of the box', OPTIONS, async ($, on) => {
  const { clock } = world(on, TMUX_ENV)
  const b = bashBox(on, 'my draft', { submits: false })
  const acks = await runShell($, on, clock, '! ls')
  expect(acks).toEqual([{ id: '1', error: 'claude did not take the command: the draft was dropped' }])
  expect(b.box).toEqual({ text: ' ls', bash: true })
})

function shellEvents(sent: Sent[]) {
  return sent.filter(s => s.body.event === 'Shells').map(s => (s.body.payload as Record<string, unknown>).shells as { id: string }[])
}

const lastShellIds = (sent: Sent[]) => shellEvents(sent).at(-1)?.map(s => s.id)

const BG = {
  session_id: 's1', transcript_path: '/t/s1.jsonl', cwd: '/w', tool_name: 'Bash', tool_use_id: 't1',
  tool_input: { command: 'make test', description: 'run tests' }, tool_response: { backgroundTaskId: 'b1' },
}

// The classic hooks beneath the mod, registered before the test first calls $.
function classicBottom(on: On) {
  on('classic.PostToolUse', () => ({}))
  on('classic.Stop', () => ({}))
  on('classic.SessionStart', () => ({}))
  on('session.append', ($, e, next) => next(e))
}

async function startShells($: EngineInterface, ...ids: string[]) {
  for (const id of ids) {
    await $.classic.PostToolUse({ ...BG, tool_use_id: `t-${id}`, tool_response: { backgroundTaskId: id } } as never)
  }
}

const notification = (id: string) =>
  `<task-notification>\n<task-id>${id}</task-id>\n<status>completed</status>\n</task-notification>`

function appendRow(text: string, agentId?: string) {
  return {
    message: { type: 'user', role: 'user', content: [{ type: 'text', text }] },
    door: 'delivery',
    origin: { kind: 'task-notification' },
    uuid: 'u1',
    ...(agentId ? { agentId } : {}),
  }
}

test('a backgrounded Bash call is reported', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostToolUse', () => ({}))
  await $.classic.PostToolUse(BG as never)
  expect(shellEvents(sent)).toEqual([[{ id: 'b1', tool_use_id: 't1', command: 'make test', desc: 'run tests' }]])
  expect(shellEvents(sent).length).toBe(1)
})

test('a foreground Bash call is not reported', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostToolUse', () => ({}))
  await $.classic.PostToolUse({ ...BG, tool_response: { stdout: 'ok' } } as never)
  expect(shellEvents(sent).length).toBe(0)
})

test("a subagent's backgrounded Bash call is not reported", OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  on('classic.PostToolUse', () => ({}))
  await $.classic.PostToolUse({ ...BG, agent_id: 'a1' } as never)
  expect(shellEvents(sent).length).toBe(0)
})

test('a task notification ends its shell', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1')
  await $.session.append(appendRow(notification('b1'), 'a1') as never)
  expect(lastShellIds(sent)).toEqual(['b1'])
  await $.session.append(appendRow(notification('b1')) as never)
  expect(lastShellIds(sent)).toEqual([])
})

test('KillShell and TaskStop end their shell', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1', 'b2')
  await $.classic.PostToolUse({ ...BG, tool_name: 'KillShell', tool_input: { shell_id: 'b1' }, tool_response: {} } as never)
  expect(lastShellIds(sent)).toEqual(['b2'])
  await $.classic.PostToolUse({ ...BG, tool_name: 'TaskStop', tool_input: { task_id: 'b2' }, tool_response: {} } as never)
  expect(lastShellIds(sent)).toEqual([])
})

test('Stop drops shells it no longer lists and never adds', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1', 'b2')
  await $.classic.Stop({
    session_id: 's1', transcript_path: '/t/s1.jsonl', cwd: '/w', stop_hook_active: false,
    background_tasks: [
      { id: 'b2', type: 'shell', status: 'running', description: '' },
      { id: 'x9', type: 'shell', status: 'running', description: '' },
    ],
  } as never)
  expect(lastShellIds(sent)).toEqual(['b2'])
})

for (const source of ['clear', 'resume']) {
  test(`${source} empties the shells, compact does not`, OPTIONS, async ($, on) => {
    world(on)
    engineSteps(on)
    on('agent.list', () => ({ value: [] as Listed[] }) as never)
    const sent = argusd(on, () => ({}))
    classicBottom(on)
    await startShells($, 'b1')
    await $.classic.SessionStart({ session_id: 's1', source: 'compact', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never)
    expect(lastShellIds(sent)).toEqual(['b1'])
    await $.classic.SessionStart({ session_id: 's2', source, transcript_path: '/t/s2.jsonl', cwd: '/w' } as never)
    expect(lastShellIds(sent)).toEqual([])
  })
}

test('classic session start re-sends the shells', OPTIONS, async ($, on) => {
  world(on)
  engineSteps(on)
  on('agent.list', () => ({ value: [] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1')
  await $.classic.SessionStart({ session_id: 's1', source: 'startup', transcript_path: '/t/s1.jsonl', cwd: '/w' } as never)
  expect(shellEvents(sent).map(e => e.map(s => s.id))).toEqual([['b1'], ['b1']])
})

test('TaskStop by its deprecated shell_id ends the shell', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1')
  await $.classic.PostToolUse({ ...BG, tool_name: 'TaskStop', tool_input: { shell_id: 'b1' }, tool_response: {} } as never)
  expect(lastShellIds(sent)).toEqual([])
})

test('one row with several notifications ends each shell', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1', 'b2', 'b3')
  await $.session.append(appendRow(`${notification('b1')}\n${notification('b3')}`) as never)
  expect(lastShellIds(sent)).toEqual(['b2'])
})

test("a model's or tool's text quoting a notification ends nothing", OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1')
  await $.session.append({ ...appendRow(notification('b1')), origin: { kind: 'model', model: 'm' } } as never)
  await $.session.append({ ...appendRow(notification('b1')), origin: { kind: 'tool', tool: 'Read' } } as never)
  expect(lastShellIds(sent)).toEqual(['b1'])
})

test("a shell's command is reported as its first line, cut to 200 characters", OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await $.classic.PostToolUse({ ...BG, tool_input: { command: "cat <<'EOF' > a.txt\nbody\nEOF" } } as never)
  await $.classic.PostToolUse({ ...BG, tool_use_id: 't2', tool_input: { command: 'x'.repeat(300) }, tool_response: { backgroundTaskId: 'b2' } } as never)
  const last = shellEvents(sent).at(-1) as unknown as { command: string }[]
  expect(last.map(s => s.command)).toEqual(["cat <<'EOF' > a.txt", 'x'.repeat(200)])
})

test('Stop drops a shell it lists as finished', OPTIONS, async ($, on) => {
  world(on)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  await startShells($, 'b1', 'b2', 'b3')
  await $.classic.Stop({
    session_id: 's1', transcript_path: '/t/s1.jsonl', cwd: '/w', stop_hook_active: false,
    background_tasks: [
      { id: 'b1', type: 'shell', status: 'completed', description: '' },
      { id: 'b2', type: 'shell', status: 'running', description: '' },
      { id: 'b3', type: 'shell', status: 'killed', description: '' },
    ],
  } as never)
  expect(lastShellIds(sent)).toEqual(['b2'])
})

test('every main turn end re-sends the shells', OPTIONS, async ($, on) => {
  world(on)
  on('agent.list', () => ({ value: [] as Listed[] }) as never)
  const sent = argusd(on, () => ({}))
  classicBottom(on)
  on('turn.complete', ($, e) => ({ text: e.answer }) as never)
  await startShells($, 'b1')
  await $.turn.complete(TURN)
  await $.turn.complete({ ...TURN, agentId: 'a1' })
  await $.turn.complete(TURN)
  expect(shellEvents(sent).map(e => e.map(s => s.id))).toEqual([['b1'], ['b1'], ['b1']])
})
