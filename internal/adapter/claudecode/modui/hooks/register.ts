import type { Color, Register, RenderElement } from 'claude-code'

// A separate plugin from argus: Claude Code skips a plugin's hooks for what its
// own code raised, and argus submits the prompts these rows draw.
export const register: Register = on => {
  // argus types its messages as <argus-message> blocks for Claude to read; the
  // person sees them drawn. ctrl+o still shows the raw prompt.
  on('ui.render', { component: 'UserMessage' }, ($, e, next) => {
    const blocks = e.props.isExpanded ? [] : argusMessages(e.props.text)
    if (blocks.length === 0) return next(e)
    const { Box, Text, Markdown } = $.ui.resolve(e)
    const rows = blocks.map(b => {
      const style = KIND[b.kind] ?? { label: b.kind, color: 'subtle' as Color, markdown: false }
      const heading = h(Box, { flexDirection: 'row', gap: 1 },
        h(Text, { bold: true, color: style.color }, style.label),
        h(Text, null, b.who),
        ...(b.session ? [h(Text, { dimColor: true }, b.session)] : []),
        h(Text, { dimColor: true }, b.id),
      )
      // Notices carry tool input whose lines and indents Markdown would reflow.
      const body = style.markdown
        ? [h(Markdown, { text: b.body })]
        : b.body.split('\n').map(line => h(Text, null, line))
      return h(Box, { flexDirection: 'column', borderStyle: 'round', borderColor: style.color, paddingX: 1 }, heading, ...body)
    })
    if (blocks.length > 1) rows.unshift(h(Text, { dimColor: true }, `argus · ${blocks.length} messages`))
    return h(Box, { flexDirection: 'column' }, ...rows) as RenderElement
  })
}

type ArgusMessage = { kind: string; who: string; session: string; id: string; body: string }

const BLOCK = /<argus-message ([^>]*)>\n([\s\S]*?)\n<\/argus-message>/g
const ATTR = /([a-z-]+)="([^"]*)"/g

// Theme keys, so the colors follow the person's theme.
const KIND: Record<string, { label: string; color: Color; markdown: boolean }> = {
  text: { label: 'message', color: 'success', markdown: true },
  task: { label: 'task', color: 'suggestion', markdown: true },
  reply: { label: 'reply', color: 'success', markdown: true },
  interaction: { label: 'prompt', color: 'permission', markdown: false },
  turn_ended: { label: 'turn ended', color: 'subtle', markdown: false },
  interaction_ended: { label: 'prompt ended', color: 'subtle', markdown: false },
}

function unescape(s: string): string {
  return s.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&amp;/g, '&')
}

// argusMessages parses the blocks internal/orch/render.go writes; the footer
// after the last block is Claude's and is left out.
function argusMessages(text: string): ArgusMessage[] {
  if (!text.startsWith('<argus-message ')) return []
  return [...text.matchAll(BLOCK)].map(([, attrs = '', body = '']) => {
    const a: Record<string, string | undefined> = Object.fromEntries(
      [...attrs.matchAll(ATTR)].map(([, k = '', v = '']) => [k, unescape(v)]),
    )
    return {
      kind: a.kind ?? '',
      who: a.child ?? a.from ?? '',
      session: (a.child ? a['child-session'] : a.session) ?? '',
      id: a.id ?? '',
      body: body.replaceAll('&lt;/argus-message>', '</argus-message>'),
    }
  })
}
