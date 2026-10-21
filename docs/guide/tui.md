# TUI

The TUI is the terminal interface of Argus. Open it with `argus`. It shows a live
list of your agent sessions (Claude Code, Codex, Antigravity, and OpenCode). You can read
transcripts, watch screens, and control sessions.

<DemoVideo src="/screenshots/demo-tui.mp4" alt="Argus TUI: session list, transcript, and history across many machines" />

```sh
argus
```

See [Single Machine](/guide/single-machine) for the node it connects to,
[Multi Machine](/guide/multi-machine) for reaching a remote gateway, and run
`argus --help` for every flag.

## Views

- **Sessions**: the session list, with live status. You can group it by host,
  project, workspace, agent, or status.
- **Left sidebar**: your projects and workspaces, or every live session with
  the ones that need you first. Open a session from either. You can limit the
  sessions to the workspace of the session you have open.
- **Transcript**: the full conversation, with foldable sections and tool-call details.
- **Live screen**: a session's terminal. You can type into it.
- **History**: past projects and sessions. You can resume a session if its
  directory still exists. In History, `/` filters only the sessions that are
  loaded. Press `m` to load more.
- **Terminals**: shells on a node or in a workspace.

## Sessions

A Claude Code session takes a prompt while it works if the
[Argus plugin](/getting-started/installation#connect-your-agents) is installed.
The prompt waits above the input until Claude Code takes it. A `/` command
waits until the turn ends. Other agents take a prompt only when they are idle.
No agent takes a prompt while a permission request, a question, or a plan waits
for you.

`r` renames a session in the agent too. For Claude Code, rename needs the Argus
plugin, and the new name applies after the current turn.

## Keys

Press `g?` in a list or the sidebar to see every key and its command. The keys follow Vim, and you can
change them. See
[Keymaps](#keymaps).

Press `:` to open the command line. Type a command name and press `<CR>` to
run it. The command line offers only the commands that do something in the
current view.

In a transcript, each run of thinking and tool calls is folded. Press `l` or
`<CR>` on a folded run, or click it, to expand it. Press `h` or `<CR>` on
either end of an expanded run to fold it again. To show runs expanded by
default, set `tui.verbose-transcript` to `true`, or run the
`toggle verbose-transcript` command. The command applies until you quit the TUI.

In a live Claude Code session, run `open subagent` to pick one of the subagents
that the session runs now and open its transcript. Run `open shell` to pick one
of the session's background shells and open the call that started it.

### Select and copy

In a transcript or an entry's detail, `y` copies the selected entry as the
agent or you wrote it.

To copy part of the text, press `v` to select by character or `V` to select by
line, move with the Vim motions `h` `j` `k` `l`, `w` `b` `e`, `0` `^` `$`, `gg`
`G`, and the page keys, and press `y` to copy. `o` jumps to the other end of
the selection, and `<Esc>` cancels. A line selection copies the text as it was
written, with its markdown and original lines; a character selection copies the
text as shown.

With the mouse on, drag across text to select it, then press `y` or open the
menu with a right click to copy. Copying sets the system clipboard, and the
terminal's clipboard too, which works over SSH.

To use your terminal's own selection instead, hold `Shift` (in most terminals)
while you drag, or give the mouse back to the terminal: set `tui.mouse` to
`off`, or run the `toggle mouse` command, which applies until you quit the TUI.

### Palette

Press `<C-k>` to open the palette. The palette searches sessions, workspaces,
projects, and nodes. Press `<CR>` to open the selected item. The palette starts
at your current location. For example, in a workspace it lists only the
sessions of that workspace. If the query is empty, press `<BS>` to search one
level wider. Press `<Tab>` on a project, a node, or a workspace to search only
in it.

Type `>` at the start of the query to search commands instead. The palette
lists the same commands as the command line, and `<CR>` runs the selected
command.

## Keymaps

To change a key, add a mapping under `tui.keymap` in the
[config file](/getting-started/configuration#config-file). `argus view` uses the
same keymaps.

```yaml
tui:
  leader-key: "<Space>"
  key-timeout: 1s
  mouse: on
  verbose-transcript: false
  keymap:
    global:
      "<C-q>": ":quit<CR>"
      "<C-n>": j
    transcript:
      "gg": "<Nop>"
```

A mapping has a key on the left and keys on the right, as in Vim:

- `:command name<CR>` runs a command. To find a command name, press `g?`. The
  help shows the command name next to each key. The [command table](#commands)
  lists every command.
- A key or a key sequence, for example `j` or `<C-w>l`, does what that key does
  as a built-in key in the section. It never runs another mapping, so
  `"<C-n>": j` moves down even if you map `j` to something else.
- `<Nop>` removes the key.

A mapping can also be an object with an `action` and a `desc`. The `action`
takes any of the forms above. The `desc` names the mapping in the footer and
in the help:

```yaml
tui:
  keymap:
    global:
      "<Leader>g":
        action: ":open palette<CR>"
        desc: go to
```

Some keys are built in: the Vim keys, such as `h`, `j`, `k`, `l`, the arrows,
`gg`, `G`, `<C-u>`, `<C-d>`, `<C-b>`, `<C-f>`, and `<C-w>h`. The other default
keys come from a default keymap, for example `P` runs `:toggle pin<CR>`. The
[command table](#commands) lists both.

- The mappings are grouped by [section](#sections). A mapping in `global`
  applies in every section that has its command or built-in key.
- A mapping adds a key. The other keys of the command stay.
- Your mappings replace a default mapping or a built-in key with the same key.
  If a section and `global` map the same key, the section wins.

### Sections

| Section | Where the keys work |
|---|---|
| `project-tree` | The tree of projects and workspaces |
| `sidebar-sessions` | The sessions in the left sidebar |
| `workspace` | The session list of a workspace |
| `project` | The summary of a project |
| `node` | The summary of a node |
| `home` | The Home sessions |
| `history` | History |
| `logs` | Logs |
| `transcript` | A session transcript, its entry detail, and its redaction list |
| `visual` | Selecting text in a session transcript or its entry detail |
| `session-dock` | The reply, question, or permission prompt of a session |
| `spawn` | The new session flow |
| `file` | An open file or diff |
| `file-tree` | The Files tab |
| `changes` | The Changes tab |
| `terminals` | The terminals of Home, a node, or a workspace |
| `global` | Every section |

### Key notation

::: v-pre
| Form | Meaning |
|---|---|
| `g`, `G`, `.`, `?` | A plain character. Case matters. |
| `g.`, `gg`, `g<C-b>` | A sequence: the keys in order. |
| `<C-x>` | ctrl+x |
| `<M-x>`, `<A-x>` | alt+x |
| `<S-x>` | shift+x |
| `<D-x>` | super+x |
| `<C-S-b>`, `<M-C-t>` | More than one modifier, in any order. |
| `<CR>`, `<Enter>`, `<Return>` | enter |
| `<Esc>` | escape |
| `<Space>` | space |
| `<Tab>`, `<S-Tab>` | tab, shift+tab |
| `<BS>` | backspace |
| `<Up>`, `<Down>`, `<Left>`, `<Right>` | arrows |
| `<PageUp>`, `<PageDown>`, `<Home>`, `<End>` | paging keys |
| `<F1>` to `<F12>` | function keys |
| `<lt>` | the `<` character |
| `<Leader>` | the leader key (set by `tui.leader-key`, default `<Space>`) |
| `<Nop>` | no key: removes the mapping |
:::

Key names and modifier letters are not case-sensitive, for example `<cr>` is
`<CR>`, and `<C-B>` is `<C-b>`. A plain character is case-sensitive, and so is
a letter with alt: `<M-a>` and `<M-A>` are different keys.

### Leader key

`<Leader>` stands for the leader key. The default is `<Space>`. To change it, set
`tui.leader-key` to one key. In a text input, the leader key types itself.

### Key sequences

A mapping can be a sequence of keys, for example `gg`. If a key starts a longer
sequence, press `<Esc>` to cancel.

If a key is a complete mapping and also starts a longer one, the TUI waits for
the next key. `tui.key-timeout` sets the wait. The default is `1s`.

### Limits

- A mapping cannot start with `:`. The `:` key opens the command line.
- The right side runs one command or one built-in key. `:a<CR>:b<CR>` and
  `ggj` are not supported.
- A removed key (`<Nop>`) takes no `desc`.
- In a text input, only single keys work.
- Yes/no prompts and the live screen do not use keymaps.
- Some keys need a terminal with the
  [Kitty keyboard protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/),
  for example a key with two modifiers. If the terminal does not support it,
  the TUI shows a warning at startup.
- A bad mapping is skipped, and the other mappings still load. At startup, the
  footer shows the first error.

### Commands

<!-- keymap-commands:start -->
| Command | Sections | Built-in keys | Default mappings |
|---|---|---|---|
| `answer note` | session-dock | `n` |  |
| `answer submit` | session-dock | `<CR>` |  |
| `back` | changes, file, file-tree, history, home, logs, node, project, project-tree, session-dock, settings, sidebar-sessions, spawn, terminals, transcript, visual, workspace | `<Esc>` |  |
| `copy` | transcript, visual | `y` |  |
| `cursor down` | visual | `<Down>` `j` |  |
| `cursor first-non-blank` | visual | `^` |  |
| `cursor left` | visual | `<Left>` `h` |  |
| `cursor line-end` | visual | `$` |  |
| `cursor line-start` | visual | `0` |  |
| `cursor right` | visual | `<Right>` `l` |  |
| `cursor up` | visual | `<Up>` `k` |  |
| `cursor word-end` | visual | `e` |  |
| `cursor word-next` | visual | `w` |  |
| `cursor word-prev` | visual | `b` |  |
| `cycle group-by` | home |  | `zg` |
| `filter-directories` | spawn | `/` |  |
| `filter-projects` | history, node, project, project-tree | `/` |  |
| `filter-sessions` | history, home, sidebar-sessions, workspace | `/` |  |
| `focus down` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w><C-j>` `<C-w><Down>` `<C-w>j` |  |
| `focus left` | changes, file, file-tree, history, home, logs, node, project, project-tree, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w><C-h>` `<C-w><Left>` `<C-w>h` |  |
| `focus next` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w><C-w>` `<C-w>w` |  |
| `focus prev` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w>W` |  |
| `focus prompt` | file, transcript |  | `<Tab>` |
| `focus right` | changes, file, file-tree, history, home, logs, node, project, project-tree, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w><C-l>` `<C-w><Right>` `<C-w>l` |  |
| `focus transcript` | session-dock | `<Tab>` | `<Tab>` |
| `focus up` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace | `<C-w><C-k>` `<C-w><Up>` `<C-w>k` |  |
| `fold close` | changes, file-tree, project-tree, transcript | `<Left>` `h` `zc` |  |
| `fold open` | changes, file-tree, project-tree, transcript | `<Right>` `l` `zo` |  |
| `goto bottom` | changes, file, file-tree, history, home, logs, project-tree, settings, sidebar-sessions, spawn, terminals, transcript, visual, workspace | `G` |  |
| `goto top` | changes, file, file-tree, history, home, logs, project-tree, settings, sidebar-sessions, spawn, terminals, transcript, visual, workspace | `gg` |  |
| `help` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), history (project list), home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, workspace |  | `g?` |
| `hover` | changes, file-tree, project-tree, sidebar-sessions |  | `K` |
| `next` | changes, file-tree, history, home, project-tree, session-dock, settings, sidebar-sessions, spawn, terminals, transcript, workspace | `<Down>` `j` |  |
| `next diff-file` | file |  | `]f` |
| `next prompt` | transcript | `}` |  |
| `node wakelock` | node, project-tree |  | `w` |
| `open` | changes, file-tree, history, home, project-tree, sidebar-sessions, spawn, terminals, transcript, workspace | `<CR>` |  |
| `open live-screen` | session-dock, transcript |  | `<C-t>` |
| `open palette` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  | `<C-k>` |
| `open setup-log` | project-tree, workspace |  | `L` |
| `open shell` | session-dock, transcript |  |  |
| `open subagent` | session-dock, transcript |  |  |
| `open tmux-pane` | home, workspace |  | `O` |
| `prev` | changes, file-tree, history, home, project-tree, session-dock, settings, sidebar-sessions, spawn, terminals, transcript, workspace | `<Up>` `k` |  |
| `prev diff-file` | file |  | `[f` |
| `prev prompt` | transcript | `{` |  |
| `project forget` | project-tree |  | `F` |
| `project rename` | project-tree |  | `r` |
| `quit` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), home, node, project, project-tree, settings, sidebar-sessions, terminals, workspace |  | `Q` |
| `redaction add` | transcript |  | `d` |
| `redaction list` | transcript |  | `D` |
| `redaction remove` | transcript |  | `u` |
| `redaction save` | transcript |  | `W` |
| `refresh` | changes, file, file-tree, history, home, node, project, project-tree, settings, sidebar-sessions, terminals, workspace |  | `gr` |
| `scroll down` | file, logs, settings, transcript | `<Down>` `j` |  |
| `scroll half-page-down` | changes, file, file-tree, history, home, logs, project-tree, session-dock, settings, sidebar-sessions, spawn, terminals, transcript, visual, workspace | `<C-d>` |  |
| `scroll half-page-up` | changes, file, file-tree, history, home, logs, project-tree, session-dock, settings, sidebar-sessions, spawn, terminals, transcript, visual, workspace | `<C-u>` |  |
| `scroll page-down` | changes, file, file-tree, history, home, logs, project-tree, session-dock, settings, spawn, terminals, transcript, visual, workspace | `<C-f>` `<PageDown>` |  |
| `scroll page-up` | changes, file, file-tree, history, home, logs, project-tree, session-dock, settings, spawn, terminals, transcript, visual, workspace | `<C-b>` `<PageUp>` |  |
| `scroll up` | file, logs, settings, transcript | `<Up>` `k` |  |
| `session kill` | home, sidebar-sessions, workspace |  | `dd` |
| `session load-more` | history |  | `m` |
| `session rename` | home, sidebar-sessions, workspace |  | `r` |
| `session resume` | history, transcript |  | `R` |
| `session spawn` | home, project-tree, sidebar-sessions, workspace |  | `s` |
| `settings` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  | `g,` |
| `settings details` | settings | `i` |  |
| `settings log` | settings | `L` |  |
| `settings pair` | settings | `a` |  |
| `settings pin` | settings | `p` |  |
| `settings remove` | settings |  | `dd` |
| `settings sign` | settings | `s` |  |
| `settings sign-tui` | settings | `S` |  |
| `settings unpin` | settings | `P` |  |
| `sidebar narrower` | changes, file-tree, node, project, project-tree, sidebar-sessions, workspace | `<C-w><lt>` |  |
| `sidebar wider` | changes, file-tree, node, project, project-tree, sidebar-sessions, workspace | `<C-w>>` |  |
| `tab next` | changes, file-tree, history, home, logs, node, project-tree, session-dock, settings, sidebar-sessions, terminals, workspace | `<Right>` `gt` |  |
| `tab prev` | changes, file-tree, history, home, logs, node, project-tree, session-dock, settings, sidebar-sessions, terminals, workspace | `<Left>` `gT` |  |
| `terminal kill` | terminals |  | `dd` |
| `terminal rename` | terminals |  | `r` |
| `terminal spawn` | terminals |  | `a` |
| `toggle active-only` | home, workspace |  | `za` |
| `toggle diff-vs-target` | changes |  | `t` |
| `toggle hidden` | project-tree |  | `H` |
| `toggle left-sidebar` | changes, file, file-tree, history, home, logs, node, project, project-tree, sidebar-sessions, terminals, transcript, visual, workspace |  | `<Leader>o` |
| `toggle line-wrap` | file |  | `yow` |
| `toggle mouse` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  |  |
| `toggle pin` | project-tree |  | `P` |
| `toggle right-sidebar` | changes, file, file-tree, history, home, logs, node, project, project-tree, sidebar-sessions, terminals, transcript, visual, workspace |  | `<Leader>e` |
| `toggle selection` | session-dock | `<Space>` |  |
| `toggle show-gone` | node, project, project-tree, workspace |  | `zx` |
| `toggle show-hidden` | node, project, project-tree, workspace |  | `z.` |
| `toggle verbose-transcript` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  |  |
| `toggle workspace-only` | sidebar-sessions |  | `zw` |
| `transcript export` | history, transcript |  | `E` |
| `upgrade gateway` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  |  |
| `upgrade node` | changes, file, file-tree, history, home, logs, node, project, project-tree, settings, sidebar-sessions, terminals, transcript, visual, workspace |  |  |
| `visual char` | transcript, visual | `v` |  |
| `visual line` | transcript, visual | `V` |  |
| `visual swap` | visual | `o` |  |
| `workspace change-target` | project-tree |  | `T` |
| `workspace force-remove` | project-tree |  | `D` |
| `workspace new` | project-tree |  | `a` |
| `workspace pick-target` | project-tree | `<C-t>` |  |
| `workspace remove` | project-tree |  | `dd` |
| `workspace rerun-setup` | project-tree |  | `S` |
<!-- keymap-commands:end -->

## Export & View

You can export any past session to a self-contained `.argus` bundle. Open it
later without a running node, gateway, or config.

Export a session from **History**. The `.argus` file goes in the current working
directory.

View an exported session bundle:

```sh
argus view session.argus
```

`.argus` files hold the session's raw transcript, including full tool input and output.
Share them only with people you trust. To strip secrets first, view with `--redact`
and save a scrubbed copy.

```sh
argus view session.argus --redact
```
