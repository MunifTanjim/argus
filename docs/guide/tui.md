# TUI

The TUI is Argus's terminal interface — open it with `argus`. It shows a live list
of your agent sessions — Claude Code, Codex, and Antigravity — and lets you read
transcripts, watch screens, and control sessions.

<DemoVideo src="/screenshots/demo-tui.mp4" alt="Argus TUI — session list, transcript, and history across a fleet" />

```sh
argus
```

See [Single Machine](/guide/single-machine) for the node it connects to,
[Multi Machine](/guide/multi-machine) for reaching a remote gateway, and run
`argus --help` for every flag.

## Views

- **Sessions** — the session list, with live status at a glance.
- **Transcript** — the full conversation, foldable, with tool-call detail.
- **Live screen** — watch a session's terminal and type into it.
- **History** — browse past projects and sessions, and resume one to pick it back up.

## Keys

Press `g?` to see every key and its command. The footer shows hints for the
current view. The keys follow Vim, and you can change them. See
[Keymaps](#keymaps).

Press `:` to open the command line. Type a command name and press `<CR>` to
run it. The list above the command line shows the matching commands and their
keys. The command line offers only the commands that do something in the
current view.

The TUI captures the mouse. To select text, hold `Shift` (in most terminals)
while you drag. To give the mouse back to the terminal, set `tui.mouse` to
`off`, or run the `toggle mouse` command for the current session.

### Palette

Press `<C-k>` to open the palette. The palette searches sessions, workspaces,
projects, and nodes. Press `<CR>` to open the selected item. The palette starts
in the place that you are in. For example, in a workspace it lists only the
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
  keymap:
    global:
      "<C-q>": quit
    transcript:
      "gg": ""
```

- A mapping has a key on the left and a command name on the right. To find a
  command name, press `g?`. The help shows the command name next to each key.
  The [command table](#commands) lists every command.
- The mappings are grouped by [section](#sections). A mapping in `global`
  applies in every section that has the command.
- A mapping adds a key. The default keys of the command stay.
- To remove a default key, map it to `""`.
- If a section and `global` map the same key, the section wins.

### Sections

| Section | Where the keys work |
|---|---|
| `project-tree` | The tree of projects and workspaces |
| `workspace` | The session list of a workspace |
| `project` | The summary of a project |
| `node` | The summary of a node |
| `home` | The Home sessions |
| `history` | History |
| `logs` | Logs |
| `transcript` | A session transcript, its card detail, and its redaction list |
| `session-dock` | The reply, question, or permission prompt of a session |
| `file` | An open file or diff |
| `file-tree` | The Files tab |
| `changes` | The Changes tab |
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
:::

Key names and modifier letters are not case-sensitive, for example `<cr>` is
`<CR>`, and `<C-B>` is `<C-b>`. A plain character is case-sensitive, and so is
a letter with alt: `<M-a>` and `<M-A>` are different keys.

### Leader key

`<Leader>` stands for the leader key. The default is `<Space>`. To change it, set
`tui.leader-key` to one key. In a text input, the leader key types itself.

### Key sequences

A mapping can be a sequence of keys, for example `gg`. If a key starts a longer
sequence, the footer shows the keys that can follow. Press `<Esc>` to cancel.

If a key is a complete mapping and also starts a longer one, the TUI waits for
the next key. `tui.key-timeout` sets the wait. The default is `1s`.

### Limits

- A mapping cannot start with `:`. The `:` key opens the command line.
- In a text input, only single keys work.
- Yes/no prompts and the live screen do not use keymaps.
- Some commands share a default key and run the one that applies. For example,
  `P` pins a project or unpins a pinned project. If you move a shared key, map
  both commands.
- Some keys need a terminal with the
  [Kitty keyboard protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/),
  for example a key with two modifiers. If the terminal does not support it,
  the TUI shows a warning at startup.
- A bad mapping is skipped, and the other mappings still load. At startup, the
  footer shows the first error.

### Commands

<!-- keymap-commands:start -->
| Command | Sections | Default keys |
|---|---|---|
| `answer submit` | session-dock | `<CR>` |
| `back` | changes, file, file-tree, history, home, logs, node, project, project-tree, session-dock, terminals, transcript, workspace | `<Esc>` |
| `filter-projects` | node, project, project-tree | `/` |
| `filter-sessions` | home, workspace | `/` |
| `focus down` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w><C-j>` `<C-w>j` |
| `focus left` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w><C-h>` `<C-w>h` |
| `focus next` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w><C-w>` `<C-w>w` |
| `focus prev` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w>W` |
| `focus prompt` | file, transcript | `<Tab>` |
| `focus right` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w><C-l>` `<C-w>l` |
| `focus transcript` | session-dock | `<Tab>` |
| `focus up` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-w><C-k>` `<C-w>k` |
| `fold close` | changes, file-tree, project-tree, transcript | `<Left>` `h` `zc` |
| `fold open` | changes, file-tree, project-tree, transcript | `<Right>` `l` `zo` |
| `goto bottom` | changes, file, file-tree, history, home, logs, project-tree, terminals, transcript, workspace | `G` |
| `goto top` | changes, file, file-tree, history, home, logs, project-tree, terminals, transcript, workspace | `gg` |
| `help` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), history (project list), home, logs, node, project, project-tree, terminals, workspace | `g?` |
| `next` | changes, file-tree, history, home, project-tree, session-dock, terminals, transcript, workspace | `<Down>` `j` |
| `next card` | transcript | `}` |
| `next diff-file` | file | `]f` |
| `open` | changes, file-tree, history, home, project-tree, terminals, transcript, workspace | `<CR>` |
| `open live-screen` | session-dock, transcript | `<C-t>` |
| `open palette` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<C-k>` |
| `open setup-log` | project-tree, workspace | `L` |
| `open tmux-pane` | home, workspace | `O` |
| `option select` | session-dock | `<Space>` |
| `option unselect` | session-dock | `<Space>` |
| `prev` | changes, file-tree, history, home, project-tree, session-dock, terminals, transcript, workspace | `<Up>` `k` |
| `prev card` | transcript | `{` |
| `prev diff-file` | file | `[f` |
| `project forget` | project-tree | `F` |
| `project hide` | project-tree | `H` |
| `project pin` | project-tree | `P` |
| `project rename` | project-tree | `r` |
| `project unhide` | project-tree | `H` |
| `project unpin` | project-tree | `P` |
| `quit` | changes (over the workspace pane), file (over the workspace pane), file-tree (over the workspace pane), home, node, project, project-tree, terminals, workspace | `Q` |
| `redaction add` | transcript | `d` |
| `redaction list` | transcript | `D` |
| `redaction remove` | transcript | `u` |
| `redaction save` | transcript | `W` |
| `refresh` | changes, file, file-tree, history, home, node, project, project-tree, terminals, workspace | `gr` |
| `scroll down` | file, logs, transcript | `<Down>` `j` |
| `scroll half-page-down` | changes, file, file-tree, history, home, logs, project-tree, session-dock, terminals, transcript, workspace | `<C-d>` `<PageDown>` |
| `scroll half-page-up` | changes, file, file-tree, history, home, logs, project-tree, session-dock, terminals, transcript, workspace | `<C-u>` `<PageUp>` |
| `scroll up` | file, logs, transcript | `<Up>` `k` |
| `session kill` | home, workspace | `dd` |
| `session load-more` | history | `m` |
| `session resume` | history, transcript | `R` |
| `session spawn` | home, project-tree, workspace | `s` |
| `sidebar narrower` | changes, file-tree, node, project, project-tree, workspace | `<C-w><lt>` |
| `sidebar wider` | changes, file-tree, node, project, project-tree, workspace | `<C-w>>` |
| `tab next` | changes, file-tree, history, home, logs, node, session-dock, terminals | `<Right>` `gt` |
| `tab prev` | changes, file-tree, history, home, logs, node, session-dock, terminals | `<Left>` `gT` |
| `terminal kill` | terminals | `dd` |
| `terminal new` | terminals | `a` |
| `terminal rename` | terminals | `r` |
| `toggle active-only` | home, workspace | `za` |
| `toggle diff-vs-target` | changes | `t` |
| `toggle left-sidebar` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<Leader>o` |
| `toggle line-wrap` | file | `yow` |
| `toggle mouse` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace |  |
| `toggle right-sidebar` | changes, file, file-tree, history, home, logs, node, project, project-tree, terminals, transcript, workspace | `<Leader>e` |
| `toggle show-gone` | node, project, project-tree, workspace | `zg` |
| `toggle show-hidden` | node, project, project-tree, workspace | `z.` |
| `transcript export` | history, transcript | `E` |
| `workspace change-target` | project-tree | `T` |
| `workspace force-remove` | project-tree | `D` |
| `workspace new` | project-tree | `a` |
| `workspace pick-target` | project-tree | `<C-t>` |
| `workspace remove` | project-tree | `dd` |
| `workspace rerun-setup` | project-tree | `S` |
<!-- keymap-commands:end -->

## Export & View

Any past session can be exported to a self-contained `.argus` bundle and opened
later with no running node, gateway, or config.

A session's transcript can be exported from **History** to a `.argus` file in the
current working directory.

View a exported session bundle:

```sh
argus view session.argus
```

`.argus` files hold the session's raw transcript — full tool input and output.
Share them only with people you trust. To strip secrets first, view with `--redact`
and save a scrubbed copy.

```sh
argus view session.argus --redact
```
