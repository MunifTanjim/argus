# Bots

A bot is a persistent assistant with its own directory. The directory holds `bot.yaml` (name, coding agent, model, instructions) and the bot's memory files. Every change to a bot's memory is kept in its history and can be undone. You can edit these files directly.

Bots are registered by name on a node. Every command accepts `--json`. The `argus bot` and `argus routine` commands reach the bots of all your connected nodes: the node they talk to passes each command on to the bot's node. When a name is on more than one node, name the bot as `<name>@<node>`, or pass `--node <node>`; the node is its ID or label. A name that is on several nodes is refused until you say which.

## Creating and registering

```sh
argus bot create scout --agent claude --model opus
argus bot create scout --from base
argus bot add ~/bots/scout
```

`create` makes the directory and registers it. The name defaults to the directory's base name. `--dir` picks the location, and `--from <bot>` copies another bot's `bot.yaml`, skills and routines (not its memory).

`add` registers an existing directory, for example a clone. It reads the name from `bot.yaml`. If the name is already taken on the node, pass `--name <new>`, which rewrites `bot.yaml`. If the directory already has a `CLAUDE.md` or `AGENTS.md`, `add` offers to import it into the bot's instructions. Without a terminal, pass `--import-instructions` to import it; the command fails otherwise and registers nothing.

Both commands take `--node <node>` to create or register the bot on another node. `--dir` and the path are then paths on that node.

## Inspecting

```sh
argus bot list
argus bot list --all
argus bot show scout
argus bot threads scout
```

`list` shows the bots of every connected node with the node each is on; `--node <node>` lists one node's bots. A node that does not answer is named in a warning, and its bots are left out. `list --all` includes forgotten bots. Each bot reports a status: `needs_you`, `waiting`, `working` or `idle`.

## Chatting

```sh
argus bot chat scout "what changed today?"
argus bot chat scout "what changed today?" --wait
argus bot chat scout --new "start fresh"
argus bot chat -m "hello"
```

A message goes to the bot's latest chat thread, or starts one if there is none or `--new` is given. The command prints the thread ID and returns. With `--wait` it waits for the turn to end (up to ten minutes) and prints the reply; with `--json` the output has `thread_id`, `session_id` and `reply`. If the bot is waiting for you (a permission or question), the command returns what it has so far and says `bot is waiting for you` (`waiting: true` in JSON).

Without a name the primary bot of the node the command talks to is used, or of `--node <node>`, else that node's only bot. If there are several and none is primary, the command asks for a name. Without a message, `chat` points you to the TUI.

On a phone with the mobile app, a link of the form `argus://bot/<bot-id>?text=<message>` opens that bot's latest chat with the message filled in. It is never sent on its own.

## Working directories

A bot's sessions run in its directory. `workdirs` in `bot.yaml` lists other directories the bot works in:

```yaml
workdirs: [~/code/api, ~/notes]
```

A path may start with `~`; a relative path is inside the bot's directory. The bot's instructions name these directories. Claude Code sessions also get access to each one that exists, and so do new Codex sessions, which may write in them. OpenCode, pi and Antigravity sessions only see them named in the instructions.

## Primary bot

```sh
argus bot primary scout
argus bot primary --unset
argus bot primary --unset --node laptop
```

Each node has one primary bot. Making a bot primary replaces its node's primary and leaves other nodes' primaries alone, so with several nodes you can have one primary on each. `--unset` clears the primary of the node the command talks to, or of `--node <node>`.

## Renaming and forgetting

```sh
argus bot rename scout scout2
argus bot forget scout
argus bot forget scout --purge
```

`forget` unregisters the bot and keeps its files and threads. Adding the same directory again restores them. `--purge` also deletes the bot's threads. Files are never deleted.

## Memory

A bot remembers what you tell it in your own chats: facts about you, your preferences and what it learns while working with you. It does not learn from routine runs or from sessions started by another bot. Each bot has its own memory, plus a profile shared by all bots.

`bots.defaults.memory.mode` sets how memory is updated, and `memory.mode` in a bot's `bot.yaml` overrides it for that bot:

- `auto`: changes are applied, you are told about them, and you can undo them.
- `review`: changes are held until you accept or reject them.
- `off`: the bot remembers nothing new, and asking it to remember something is refused.

Memory is tidied once a day. Tidying never removes or rewrites anything you wrote yourself. Anything that looks like a secret is never written to memory.

```sh
argus bot memory show scout
argus bot memory show scout --profile
```

`show` prints the bot's memory and how much of its size budget is used. `--profile` prints the shared profile instead.

### History and undo

```sh
argus bot memory log scout
argus bot memory log scout --limit 50
argus bot memory undo scout <commit>
argus bot memory undo scout <commit> --profile
```

`log` lists changes newest first, with where each came from (a chat, the bot itself, tidying, or you). `undo` reverts one change, given by the hash from `log`. `--profile` works on the shared profile's history.

### Reviewing proposals

```sh
argus bot memory review scout
argus bot memory review scout --accept <id>
argus bot memory review scout --reject <id>
```

In `review` mode, and for imports, proposed changes wait for your decision. A bot can also propose a rewrite of its own instructions; that always waits for your decision, whatever the mode. Accepting applies the change; rejecting discards it.

### Skills

A bot can write down a procedure it learned as a skill and reuse it later. Skills are shared by all bots and can be copied to a new bot with `create --from`.

### Importing

```sh
argus bot memory import scout memories.txt
cat memories.txt | argus bot memory import scout -
```

`import` reads memory exported from another assistant, from a file or standard input, and stages what it finds for review. It prints `staged for review: N`, then use `review` to accept or reject each one.

## Routines and goals

A routine is work a bot does on its own, on a schedule or when something happens. Each run is a fresh background session that ends with a short report: what it did, what is pending and what it could not verify. A routine with an outcome is a goal: the bot keeps working on it, run after run, until the outcome is reached.

### Routine files

Each routine is a file in the bot's `routines/` folder, named `<slug>.yaml`. Edit these files to add or change routines; a new routine waits for its next due time.

```yaml
what: Migrate the billing service to the new payments API.
starts:
  - schedule: "0 9 * * MON-FRI"   # 5-field cron
  - every: 2h                     # interval; 15m at the least
  - at: 2026-12-01T09:00          # once
  - check-back: self-paced        # the bot picks when to run again
  - event: webhook                # an HTTP request starts it
does: act            # act: do the work; tell: only find out and report
until: outcome       # stopped: runs until you stop it; outcome: a goal
outcome: All billing calls use the new API and the old client is deleted.
verify: make test-billing         # optional check, run in the bot's directory
budget: { sessions: 30, cost: 10.00, tokens: 0 }   # 0 is no limit
timezone: auto       # auto uses the bot's timezone
if-unavailable: report            # report | skip | wait
report-to: thread    # thread: post a summary to the bot's latest chat; quiet: don't
overlap: skip        # skip | queue: what happens when a run is still going
enabled: true
```

Only `what` and `starts` are required. A goal keeps its plan in `routines/<slug>/plan.md`, which the bot maintains, and a log of runs in `routines/<slug>/log.md`, which gets one line per run.

### Budgets

A budget limits a routine's runs, counted over all of them: the sessions they start, what those sessions cost and the tokens they use. A session's tokens are its total so far: the input, cached or not, and the output of every model call it made, as its coding CLI reports them. Claude Code, Codex, OpenCode and pi report tokens. On a bot whose CLI does not, a token budget is not enforced, and the bot's latest chat gets one notice per routine that the token budget is unavailable for that CLI; the other limits still apply. Cost counts only when the CLI reports it.

### How runs end

Each run reports one of `progress`, `no_change`, `blocked`, `needs_you` or `done`. A run that ends without a report counts as failed.

- A goal is complete when a run reports `done`. If `verify` is set, Argus runs it first (for up to 10 minutes): if it passes, the goal is complete and stops; if it fails, the result counts as progress and the next run sees the failure.
- A routine with `until: stopped` never completes; `done` counts as progress.
- `blocked` and `needs_you` tell you that the bot needs you.
- Unless `report-to` is `quiet` or nothing changed, a summary of each run appears in the bot's latest chat.

Runs never change the bot's long-term memory, but they add what they did to its work log. A run that reaches a budget limit pauses its goal. After the first failure you get a notice, and after 10 failures in a row the routine pauses.

### Check-backs

A routine with `check-back: self-paced` runs again when the bot asks, between 15 minutes and 7 days later; by default, a day later. In a chat you can also ask the bot to check back on something later; it then makes a one-time routine that removes itself after it runs. A bot can have at most 10 pending check-backs.

### Asking the bot

In a chat, the bot can list, create, change, pause, resume and delete its routines; each change can be undone with `argus bot memory undo`. A new goal, or a change to a goal's outcome or `verify`, waits for your decision in `argus bot memory review`. Only you can resume a routine that ran out of budget.

### Commands

```sh
argus routine list
argus routine list scout
argus routine show scout/billing
argus routine run scout/billing
argus routine pause scout/billing
argus routine resume scout/billing
argus routine runs scout/billing --limit 5
argus routine hook-secret scout/alerts
argus routine run scout@laptop/billing
```

Without a bot, `list` shows the routines of every bot on every connected node. A bot whose name is on several nodes is named `<bot>@<node>/<slug>`, or pass `--node <node>`. `run` starts a test run now. `pause` stops a routine without editing its file; `resume` restarts it from now and skips the times it missed. A completed goal can't be resumed. A routine with `enabled: false` in its file is off until you edit the file.

If the node was off when a routine was due, the routine runs once when the node comes back, if the missed time is less than 12 hours old. Routines pause when no client has connected for 7 days.

### Webhooks

A routine with `event: webhook` runs when something sends `POST /hooks/<bot>/<slug>` with `Authorization: Bearer <secret>` and a JSON body of at most 64 KB. The run receives the body as data to read, not as instructions. `argus routine hook-secret` makes a new secret and prints it once; the previous secret stops working.

The webhook listener is off by default. Set `bots.webhook.listen-addr` (for example `0.0.0.0:7420`) to turn it on. The sender must be able to reach that address, for example over your LAN or Tailscale; webhooks do not go through the gateway. A 200 response means the run was accepted, not that it finished. Requests over the hourly run limit get 429.

The limits above are defaults; see [Configuration](../getting-started/configuration.md).

## Notifications, briefs and offers

### Notifications

Each bot's `notify` setting decides what reaches your devices:

| What happened | `all` | `needs-you` (default) | `off` |
| --- | --- | --- | --- |
| A run needs you or is blocked, the bot asks for approval or for a hand-off, a health notice | yes | yes | no |
| A run made progress or finished, a brief, an offer | yes | no | no |
| Nothing changed, memory changes | no | no | no |

- **Quiet hours** (`quiet-hours: 22:00-07:00`): notifications from background work wait, and you get one summary when quiet hours end. Chats you started still notify you.
- **While you are connected**, a notification waits a few minutes (`notify-delay`, default 3m) and is dropped if you read the thread meanwhile.
- **Health notices** tell you about problems Argus detects: the bot's coding CLI is not installed, sessions failed to start three times in a row, or a routine paused because it failed too often, ran out of budget or you were away. Each notifies once, then at most once a day until it is resolved.

### Briefs

With `brief: { enabled: true, at: "08:00" }`, the bot writes a short daily brief at that time: what needs you, what its runs did since the last brief, what is due today and how its goals are going. Argus gathers these facts; the bot writes them up. The brief appears in the bot's latest chat. On a day with nothing to tell, there is no brief. When the node has a primary bot, only the primary writes a brief, and it covers every bot on the node.

### Proactivity and offers

`proactivity` sets how much a bot does on its own:

| | Briefs | Offers per day | Self-paced check-backs wait |
| --- | --- | --- | --- |
| `off` | no | none | only as long as each run asks, up to 7 days |
| `low` | yes | 1 | 2 days by default |
| `default` | yes | 3 | 1 day by default |
| `high` | yes | 6 | 12 hours by default |

An offer is something the bot proposes to do, with a short reason, posted to its latest chat. Starting an offer sends its prompt to the bot as if you wrote it; dismissing it drops it. An offer never does anything by itself.

### Settings and usage

```sh
argus bot set scout notify all
argus bot set scout proactivity low
argus bot set scout memory.mode review
argus bot set scout quiet-hours 22:00-07:00
argus bot set scout brief.enabled true
argus bot set scout brief.at 07:30
argus bot set scout notify ""
argus bot usage
argus bot usage scout --since 7d
```

`set` changes one setting in the bot's `bot.yaml` and keeps the change in its history; an empty value goes back to the default. `usage` shows sessions, cost and tokens per bot, split by who started the work: you (chats and test runs), a routine, a check-back or an event. Cost and tokens appear only when the bot's coding CLI reports them; tokens are each session's total, counted as for budgets.

## Bots asking bots

With `bots.coordination.enabled` on, a bot can ask another bot for work. Each bot lists the bots it may ask in its `bot.yaml`:

```yaml
can-message: [researcher, reviewer]
```

The list is empty by default. A bot that is not on the list, or does not exist, is refused the same way. Bots on this node are found first, then bots on your other nodes.

The other bot does the work in a background run, started by the hand-off, and reports like any run. Its summary goes back word for word: it appears in the asking bot's thread under the answering bot's name, and the asking bot continues from it. If the run fails, or no answer comes within `answer-timeout` (1h), the asking thread records that there was no answer.

A request can lead to further hand-offs, up to `max-depth` (3) deep and `max-requests-per-root` (12) in all. A bot is never asked again by a request it is already working on. Every refusal is recorded in the asking thread. Work started by a hand-off is unattended.

The primary bot can look over every bot on its node: what each did lately, its recent run results, and what waits for you. Any bot can propose a new bot, with a name, description and instructions; it is created only when you accept the proposal with the other memory proposals.

## Approvals

Bot sessions run in the coding agent's normal permission mode, so it asks before actions that are not read-only. Argus answers those requests for bot sessions. It never sees what the coding agent runs without asking.

### Who started the work

Work you start by messaging the bot is attended. Routines, check-backs and other work you did not start are unattended.

- Attended work: Argus asks you, unless a rule decides. It warns when an action sends data out of this machine, can't be undone or changes security settings.
- Unattended work: an action is denied unless an approval rule allows it. The bot is told to add a rule if it needs one.
- Unattended work never sends anything out on its own. Such an action becomes a draft in the thread. Sending a shell draft runs the command again, in the working directory the request named or else the bot's directory, and records its output. Sending any other draft asks the bot to do it now, and that one action is then allowed without asking. Discarding a draft drops it.
- When Argus can't see what an action does (some coding agents ask without sending the command or file), unattended work asks you instead of following rules.

### Answering

A request includes the exact action, its warnings and who started the work. You can:

- allow it once;
- allow it in this thread: the same tool and target are allowed until the thread's session ends;
- always allow it: adds a rule for this exact action (the shell command with its arguments, the file, the host or the MCP tool) to the bot's `bot.yaml` and commits it. This is not available when no rule would cover only this action, such as for a chain of shell commands or a command without arguments;
- deny it: the bot gets your reason. The same action is denied again without asking until you next message the bot;
- ask for an explanation: the request is denied and the bot is asked to explain the action in plain language, then ask again.

A new message from you closes the requests still open in that thread. A request from unattended work is denied if you don't answer within 30 minutes (`bots.approvals.unattended-expiry`).

### Rules

Rules go in the bot's `bot.yaml` under `approvals`, and in `approvals.yaml` in the profile directory for every bot on the node:

```yaml
approvals:
  - { tool: Bash, target: git, decision: allow }
  - { tool: Bash, target: "gh pr *", decision: ask }
  - { tool: "mcp__gmail__*", decision: ask, started-by: [routine, event] }
  - { class: irreversible, decision: ask }
```

`tool` and `target` are patterns where `*` matches anything. The target of a shell command is its program, or the program and its arguments when the pattern has a space. For file tools it is the path, for fetches the host, and for MCP tools the tool name. `class` is `outward`, `irreversible` or `security`. `started-by` limits a rule to work started that way (`you`, `routine`, `check-back`, `event`, `handoff`). `decision` is `allow`, `ask` or `deny`; `expires` (an RFC 3339 time) and `note` are optional. When several rules match, deny wins over ask, and ask wins over allow. No rule turns an unattended outward action into anything but a draft.

`bots.execution` sets what the node allows without a rule: `ask` (the default), `allow` or `never-unattended`. With `never-unattended`, unattended work can't run any action.

### Policy hook

`bots.approvals.hook` names a program (`command`) or an HTTPS address (`url`; plain HTTP only to this machine) that Argus asks about every bot action. A program reads the request on standard input and writes the reply on standard output; an address gets the request as a JSON POST. The request is:

```json
{"action": {"tool": "Bash", "target": "git", "class": ["outward"], "input": {"command": "git push"}},
 "bot": {"id": "...", "name": "scout"}, "thread": "...", "started_by": "routine", "session": "...", "node": "..."}
```

The reply is `{"decision": "allow" | "ask" | "deny", "reason": "..."}`. `mode` sets what the reply does:

- `shadow` (the default): the reply is only recorded.
- `fail-open`: the reply decides; if the hook fails or takes longer than `timeout` (5s), the rules decide.
- `fail-closed`: the reply decides; if the hook fails or times out, the action is denied.

The hook can't overrule drafts, or the denial of an action you already denied.

### Activity

Every decision is recorded:

```sh
argus bot activity scout
argus bot activity scout --unattended
```

### Protected files

Before Argus commits in a bot's repository, it checks that these files are as it last recorded them:

- the repository's git hooks and git config (with `bots.defaults.git.mode: outer`, those of the enclosing repository);
- the `approvals` in the bot's `bot.yaml` (other keys are not checked);
- `approvals.yaml` in the profile directory, and the git hooks and config of the profile repository.

Argus records them when you create or add the bot, and again when it changes them itself, such as when you always allow an action. A change made any other way stops Argus from committing for that bot: it does not run git, so no hook runs, and it sends a needs-you notice naming what changed. This includes your own edits made outside Argus, such as editing the approvals in `bot.yaml` or `approvals.yaml` by hand, or installing a git hook. Memory changes, run logs, settings and undo, which all commit, fail until you accept the change:

```sh
argus bot trust scout
```

This records the bot's files and the shared ones as they are now and lists the changes it accepted. A change to `approvals.yaml` or the profile repository is accepted for every bot at once. The check covers commits only: an edited rule applies to approval requests as soon as it is saved.

## Secrets

A bot can have secrets, such as API tokens. They are kept on the node, encrypted, and never written to the bot's files, memory or history.

```sh
argus bot secret set scout GH_TOKEN --description "GitHub token for issues"
argus bot secret list scout
argus bot secret rm scout GH_TOKEN
```

`set` reads the value without showing it, or from standard input when it is piped in (all of it, without one trailing newline, so multi-line values such as keys stay whole); the value is never a command-line argument. A name is like `GH_TOKEN`; names of variables the shell, the system or Argus depend on, such as `PATH`, `HOME`, `LD_*` or `ARGUS_*`, can't be used. The bot's instructions list the names and descriptions only. A bot can also ask you for a secret it needs; your answer is stored the same way, and the bot is told only the name.

A bot reads a secret when a command needs it:

```sh
GH_TOKEN=$(argus secret get GH_TOKEN) gh issue list
```

`argus secret get` prints the value with no trailing newline. It works only in a command one of the bot's sessions runs, and returns only that bot's secrets; anywhere else it fails. A secret is available as soon as it is stored. This works in Claude Code, Codex, OpenCode and pi sessions, not in Antigravity sessions. Each read is recorded in the bot's activity by name.

The bot and the tools it runs can read these values. Secrets keep values out of transcripts, memory and history; they do not hide them from the bot. Where a known secret value would appear in approval requests, drafts, activity or memory capture, Argus replaces it with the secret's name.
