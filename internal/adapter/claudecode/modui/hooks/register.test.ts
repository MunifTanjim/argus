import type { On, RenderElement } from 'claude-code'
import { expect, test } from 'claude-code/testing'

const SURFACES = ['terminal', 'desktop', 'vscode', 'mobile'] as const

const REPLY = `<argus-message id="home:m-1a2b3c4d" kind="reply" from="worker-api" session="home:argus:%5" reply-to="m-00000001">
All tests pass.
</argus-message>

Reply to a message with the argus \`reply_to_message\` tool (message_id is the message's id), or: argus session reply <id> "<answer>"`

const NOTICE = `<argus-message id="m-5e6f7a8b" kind="interaction" from="argus" child="worker-db" child-session="home:argus:%7">
worker-db (home:argus:%7) asks for permission:
Tool: Bash
Input: {
  "cmd": "rm -rf build"
}
</argus-message>`

// argus delivers each message with $.prompt.submit, so the row's origin is
// argus's own; the hook must live outside argus for it to run at all.
function userMessage(surface: (typeof SURFACES)[number], text: string, isExpanded = false) {
  return {
    plugin: 'argus-ui', surface, component: 'UserMessage',
    props: { text, origin: { kind: 'plugin', name: 'argus', asUser: true }, isExpanded },
  } as const
}

// Stands for the engine's own row, so a row the mod leaves is told apart.
function engineRow(on: On) {
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return h(Text, null, 'engine') as RenderElement
  })
}

test('an argus message row draws each block, not the raw tags', async ($, on) => {
  engineRow(on)
  for (const surface of SURFACES) {
    const ui = await $.ui.mount(userMessage(surface, `${REPLY.split('\n\nReply')[0]}\n\n${NOTICE}\n\nfooter`))
    expect((await ui.find({ type: 'Text', text: /argus · 2 messages/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^reply$/ }))?.props).toMatchObject({ bold: true, color: 'success' })
    expect((await ui.find({ type: 'Text', text: /^worker-api$/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^home:argus:%5$/ }))?.props).toMatchObject({ dimColor: true })
    expect((await ui.find({ type: 'Text', text: /^home:m-1a2b3c4d$/ }))?.props).toMatchObject({ dimColor: true })
    expect((await ui.find({ type: 'Markdown', text: /^All tests pass\.$/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^prompt$/ }))?.props).toMatchObject({ color: 'permission' })
    expect((await ui.find({ type: 'Text', text: /^worker-db$/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^home:argus:%7$/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^Tool: Bash$/ }))).toBeDefined()
    expect((await ui.find({ type: 'Text', text: /^ {2}"cmd": "rm -rf build"$/ }))).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /<argus-message|footer|engine/ })).toBeUndefined()
    expect(await ui.find({ type: 'Markdown', text: /<argus-message|footer|Tool: Bash/ })).toBeUndefined()
    const frames = (await ui.findAll({ type: 'Box' })).filter(b => b.props.borderStyle === 'round')
    expect(frames.map(b => b.props.borderColor)).toEqual(['success', 'permission'])
    await ui.unmount()
  }
})

test('a single argus message has no count line', async ($, on) => {
  engineRow(on)
  const ui = await $.ui.mount(userMessage('terminal', REPLY))
  expect(await ui.find({ type: 'Text', text: /messages/ })).toBeUndefined()
  expect(await ui.find({ type: 'Markdown', text: /^All tests pass\.$/ })).toBeDefined()
  await ui.unmount()
})

test('a message body is drawn as markdown, whole', async ($, on) => {
  engineRow(on)
  const body = '**Purpose**\nAdds things.\n\n- one\n- two'
  const ui = await $.ui.mount(userMessage('terminal', `<argus-message id="m-1" kind="task" from="lead" session="home:argus:%1">\n${body}\n</argus-message>`))
  expect((await ui.find({ type: 'Markdown' }))?.text).toBe(body)
  expect((await ui.find({ type: 'Text', text: /^task$/ }))?.props).toMatchObject({ color: 'suggestion' })
  await ui.unmount()
})

test('an unknown kind is drawn by its name, as plain text', async ($, on) => {
  engineRow(on)
  const ui = await $.ui.mount(userMessage('terminal', `<argus-message id="m-1" kind="later_kind" from="argus">\n*x*\n</argus-message>`))
  expect((await ui.find({ type: 'Text', text: /^later_kind$/ }))?.props).toMatchObject({ color: 'subtle' })
  expect(await ui.find({ type: 'Text', text: /^\*x\*$/ })).toBeDefined()
  expect(await ui.find({ type: 'Markdown' })).toBeUndefined()
  await ui.unmount()
})

test('the expanded view and other prompts keep the engine row', async ($, on) => {
  engineRow(on)
  for (const ui of [
    await $.ui.mount(userMessage('terminal', REPLY, true)),
    await $.ui.mount(userMessage('terminal', 'fix the bug')),
  ]) {
    expect((await ui.find({ type: 'Text', text: /engine/ }))?.text).toBe('engine')
    await ui.unmount()
  }
})

test('a body keeps its own entities; only escaped closing tags are restored', async ($, on) => {
  engineRow(on)
  const text = `<argus-message id="m-1" kind="text" from="w" session="home:argus:%1">
a &lt; b &amp; c&lt;/argus-message>d
</argus-message>`
  const ui = await $.ui.mount(userMessage('terminal', text))
  expect(await ui.find({ type: 'Markdown', text: /^a &lt; b &amp; c<\/argus-message>d$/ })).toBeDefined()
  await ui.unmount()
})

test('a notice line that looks like a tag is drawn as written', async ($, on) => {
  engineRow(on)
  const text = `<argus-message id="m-1" kind="interaction" from="argus" child="w" child-session="home:argus:%1">
<tool>x</tool>
</argus-message>`
  const ui = await $.ui.mount(userMessage('terminal', text))
  expect(await ui.find({ type: 'Text', text: /^<tool>x<\/tool>$/ })).toBeDefined()
  await ui.unmount()
})

test('an interaction_ended message is labeled prompt ended', async ($, on) => {
  engineRow(on)
  const ui = await $.ui.mount(userMessage('terminal', `<argus-message id="m-1" kind="interaction_ended" from="argus">\nx\n</argus-message>`))
  expect(await ui.find({ type: 'Text', text: /^prompt ended$/ })).toBeDefined()
  await ui.unmount()
})
