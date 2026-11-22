# Agents

An agent is a persistent assistant with its own directory. The directory holds `agent.yaml` (name, coding agent, model, instructions) and the agent's memory files. Every change to an agent's memory is kept in its history and can be undone. You can edit these files directly.

Agents are registered by name on a node. Every command accepts `--json`. Where two nodes have an agent with the same name, refer to it as `<name>@<node-label>`.

## Creating and registering

```sh
argus agent create scout --agent claude --model opus
argus agent create scout --from base
argus agent add ~/agents/scout
```

`create` makes the directory and registers it. The name defaults to the directory's base name. `--dir` picks the location, and `--from <agent>` copies another agent's `agent.yaml`, skills and routines (not its memory).

`add` registers an existing directory, for example a clone. It reads the name from `agent.yaml`. If the name is already taken on the node, pass `--name <new>`, which rewrites `agent.yaml`. If the directory already has a `CLAUDE.md` or `AGENTS.md`, `add` offers to import it into the agent's instructions. Without a terminal, pass `--import-instructions` to import it; the command fails otherwise and registers nothing.

Both commands take `--node <node>` to create or register the agent on another node. `--dir` and the path are then paths on that node.

## Inspecting

```sh
argus agent list
argus agent list --all
argus agent show scout
argus agent threads scout
```

`list --all` includes forgotten agents. Each agent reports a status: `needs_you`, `waiting`, `working` or `idle`.

## Chatting

```sh
argus agent chat scout "what changed today?"
argus agent chat scout "what changed today?" --wait
argus agent chat scout --new "start fresh"
argus agent chat -m "hello"
```

A message goes to the agent's latest chat thread, or starts one if there is none or `--new` is given. The command prints the thread ID and returns. With `--wait` it waits for the turn to end (up to ten minutes) and prints the reply; with `--json` the output has `thread_id`, `session_id` and `reply`. If the agent is waiting for you (a permission or question), the command returns what it has so far and says `agent is waiting for you` (`waiting: true` in JSON).

Without a name the primary agent is used, else the only agent. If there are several and none is primary, the command asks for a name. Without a message, `chat` points you to the TUI.

## Primary agent

```sh
argus agent primary scout
argus agent primary --unset
```

## Renaming and forgetting

```sh
argus agent rename scout scout2
argus agent forget scout
argus agent forget scout --purge
```

`forget` unregisters the agent and keeps its files and threads. Adding the same directory again restores them. `--purge` also deletes the agent's threads. Files are never deleted.

## Memory

An agent remembers what you tell it in your own chats: facts about you, your preferences and what it learns while working with you. It does not learn from routine runs or from sessions started by another agent. Each agent has its own memory, plus a profile shared by all agents.

`agents.defaults.memory.mode` sets how memory is updated, and `memory.mode` in an agent's `agent.yaml` overrides it for that agent:

- `auto`: changes are applied, you are told about them, and you can undo them.
- `review`: changes are held until you accept or reject them.
- `off`: the agent remembers nothing new, and asking it to remember something is refused.

Memory is tidied once a day. Tidying never removes or rewrites anything you wrote yourself. Anything that looks like a secret is never written to memory.

```sh
argus agent memory show scout
argus agent memory show scout --profile
```

`show` prints the agent's memory and how much of its size budget is used. `--profile` prints the shared profile instead.

### History and undo

```sh
argus agent memory log scout
argus agent memory log scout --limit 50
argus agent memory undo scout <commit>
argus agent memory undo scout <commit> --profile
```

`log` lists changes newest first, with where each came from (a chat, the agent itself, tidying, or you). `undo` reverts one change, given by the hash from `log`. `--profile` works on the shared profile's history.

### Reviewing proposals

```sh
argus agent memory review scout
argus agent memory review scout --accept <id>
argus agent memory review scout --reject <id>
```

In `review` mode, and for imports, proposed changes wait for your decision. An agent can also propose a rewrite of its own instructions; that always waits for your decision, whatever the mode. Accepting applies the change; rejecting discards it.

### Skills

An agent can write down a procedure it learned as a skill and reuse it later. Skills are shared by all agents and can be copied to a new agent with `create --from`.

### Importing

```sh
argus agent memory import scout memories.txt
cat memories.txt | argus agent memory import scout -
```

`import` reads memory exported from another assistant, from a file or standard input, and stages what it finds for review. It prints `staged for review: N`, then use `review` to accept or reject each one.

## Routines and goals

A routine is work an agent does on its own, on a schedule or when something happens. Each run is a fresh background session that ends with a short report: what it did, what is pending and what it could not verify. A routine with an outcome is a goal: the agent keeps working on it, run after run, until the outcome is reached.

### Routine files

Each routine is a file in the agent's `routines/` folder, named `<slug>.yaml`. Edit these files to add or change routines; a new routine waits for its next due time.

```yaml
what: Migrate the billing service to the new payments API.
starts:
  - schedule: "0 9 * * MON-FRI"   # 5-field cron
  - every: 2h                     # interval; 15m at the least
  - at: 2026-12-01T09:00          # once
  - check-back: self-paced        # the agent picks when to run again
  - event: webhook                # an HTTP request starts it
does: act            # act: do the work; tell: only find out and report
until: outcome       # stopped: runs until you stop it; outcome: a goal
outcome: All billing calls use the new API and the old client is deleted.
verify: make test-billing         # optional check, run in the agent's directory
budget: { sessions: 30, cost: 10.00, tokens: 0 }   # 0 is no limit
timezone: auto       # auto uses the agent's timezone
if-unavailable: report            # report | skip | wait
report-to: thread    # thread: post a summary to the agent's latest chat; quiet: don't
overlap: skip        # skip | queue: what happens when a run is still going
enabled: true
```

Only `what` and `starts` are required. A goal keeps its plan in `routines/<slug>/plan.md`, which the agent maintains, and a log of runs in `routines/<slug>/log.md`, which gets one line per run.

### How runs end

Each run reports one of `progress`, `no_change`, `blocked`, `needs_you` or `done`. A run that ends without a report counts as failed.

- A goal is complete when a run reports `done`. If `verify` is set, Argus runs it first (for up to 10 minutes): if it passes, the goal is complete and stops; if it fails, the result counts as progress and the next run sees the failure.
- A routine with `until: stopped` never completes; `done` counts as progress.
- `blocked` and `needs_you` tell you that the agent needs you.
- Unless `report-to` is `quiet` or nothing changed, a summary of each run appears in the agent's latest chat.

Runs never change the agent's long-term memory, but they add what they did to its work log. A run that reaches a budget limit pauses its goal. After the first failure you get a notice, and after 10 failures in a row the routine pauses.

### Check-backs

A routine with `check-back: self-paced` runs again when the agent asks, between 15 minutes and 7 days later; by default, a day later. In a chat you can also ask the agent to check back on something later; it then makes a one-time routine that removes itself after it runs. An agent can have at most 10 pending check-backs.

### Asking the agent

In a chat, the agent can list, create, change, pause, resume and delete its routines; each change can be undone with `argus agent memory undo`. A new goal, or a change to a goal's outcome or `verify`, waits for your decision in `argus agent memory review`. Only you can resume a routine that ran out of budget.

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
```

`run` starts a test run now. `pause` stops a routine without editing its file; `resume` restarts it from now and skips the times it missed. A completed goal can't be resumed. A routine with `enabled: false` in its file is off until you edit the file.

If the node was off when a routine was due, the routine runs once when the node comes back, if the missed time is less than 12 hours old. Routines pause when no client has connected for 7 days.

### Webhooks

A routine with `event: webhook` runs when something sends `POST /hooks/<agent>/<slug>` with `Authorization: Bearer <secret>` and a JSON body of at most 64 KB. The run receives the body as data to read, not as instructions. `argus routine hook-secret` makes a new secret and prints it once; the previous secret stops working.

The webhook listener is off by default. Set `agents.webhook.listen-addr` (for example `0.0.0.0:7420`) to turn it on. The sender must be able to reach that address, for example over your LAN or Tailscale; webhooks do not go through the gateway. A 200 response means the run was accepted, not that it finished. Requests over the hourly run limit get 429.

The limits above are defaults; see [Configuration](../getting-started/configuration.md).

## Notifications, briefs and offers

### Notifications

Each agent's `notify` setting decides what reaches your devices:

| What happened | `all` | `needs-you` (default) | `off` |
| --- | --- | --- | --- |
| A run needs you or is blocked, the agent asks for approval or for a hand-off, a health notice | yes | yes | no |
| A run made progress or finished, a brief, an offer | yes | no | no |
| Nothing changed, memory changes | no | no | no |

- **Quiet hours** (`quiet-hours: 22:00-07:00`): notifications from background work wait, and you get one summary when quiet hours end. Chats you started still notify you.
- **While you are connected**, a notification waits a few minutes (`notify-delay`, default 3m) and is dropped if you read the thread meanwhile.
- **Health notices** tell you about problems Argus detects: the agent's coding CLI is not installed, sessions failed to start three times in a row, or a routine paused because it failed too often, ran out of budget or you were away. Each notifies once, then at most once a day until it is resolved.

### Briefs

With `brief: { enabled: true, at: "08:00" }`, the agent writes a short daily brief at that time: what needs you, what its runs did since the last brief, what is due today and how its goals are going. Argus gathers these facts; the agent writes them up. The brief appears in the agent's latest chat. On a day with nothing to tell, there is no brief. When the node has a primary agent, only the primary writes a brief, and it covers every agent on the node.

### Proactivity and offers

`proactivity` sets how much an agent does on its own:

| | Briefs | Offers per day | Self-paced check-backs wait |
| --- | --- | --- | --- |
| `off` | no | none | only as long as each run asks, up to 7 days |
| `low` | yes | 1 | 2 days by default |
| `default` | yes | 3 | 1 day by default |
| `high` | yes | 6 | 12 hours by default |

An offer is something the agent proposes to do, shown in its chat with a short reason. Starting an offer sends its prompt to the agent as if you wrote it; dismissing it drops it. An offer never does anything by itself.

### Settings and usage

```sh
argus agent set scout notify all
argus agent set scout proactivity low
argus agent set scout memory.mode review
argus agent set scout quiet-hours 22:00-07:00
argus agent set scout brief.enabled true
argus agent set scout brief.at 07:30
argus agent set scout notify ""
argus agent usage
argus agent usage scout --since 7d
```

`set` changes one setting in the agent's `agent.yaml` and keeps the change in its history; an empty value goes back to the default. `usage` shows sessions, cost and tokens per agent, split by who started the work: you (chats and test runs), a routine, a check-back or an event. Cost and tokens appear only when the agent's coding CLI reports them.

## Agents asking agents

With `agents.coordination.enabled` on, an agent can ask another agent for work. Each agent lists the agents it may ask in its `agent.yaml`:

```yaml
can-message: [researcher, reviewer]
```

The list is empty by default. An agent that is not on the list, or does not exist, is refused the same way. Agents on this node are found first, then agents on your other nodes.

The other agent does the work in a background run, started by the hand-off, and reports like any run. Its summary goes back word for word: it appears in the asking agent's thread under the answering agent's name, and the asking agent continues from it. If the run fails, or no answer comes within `answer-timeout` (1h), the asking thread records that there was no answer.

A request can lead to further hand-offs, up to `max-depth` (3) deep and `max-requests-per-root` (12) in all. An agent is never asked again by a request it is already working on. Every refusal is recorded in the asking thread. Work started by a hand-off is unattended.

## Approvals

Agent sessions run in the coding agent's normal permission mode, so it asks before actions that are not read-only. Argus answers those requests for agent sessions. It never sees what the coding agent runs without asking.

### Who started the work

Work you start by messaging the agent is attended. Routines, check-backs and other work you did not start are unattended.

- Attended work: Argus asks you, unless a rule decides. It warns when an action sends data out of this machine, can't be undone or changes security settings.
- Unattended work: an action is denied unless an approval rule allows it. The agent is told to add a rule if it needs one.
- Unattended work never sends anything out on its own. Such an action becomes a draft in the thread. **Send** runs a shell command again in the agent's directory and records its output. For any other tool, Send asks the agent to do it now, and that one action is then allowed without asking. **Discard** drops the draft.

### Answering

A request shows the exact action, its warnings and who started the work. The answers are:

- **Allow once**
- **Allow in this thread**: the same tool and target are allowed until the thread's session ends.
- **Always allow**: adds a rule for the tool and target to the agent's `agent.yaml` and commits it.
- **Deny**: the agent gets your reason. The same action is denied again without asking until you next message the agent.
- **Explain**: denies and asks the agent to explain the action in plain language, then ask again.

A new message from you closes the requests still open in that thread. A request from unattended work is denied if you don't answer within 30 minutes (`agents.approvals.unattended-expiry`).

### Rules

Rules go in the agent's `agent.yaml` under `approvals`, and in `approvals.yaml` in the profile directory for every agent on the node:

```yaml
approvals:
  - { tool: Bash, target: git, decision: allow }
  - { tool: Bash, target: "gh pr *", decision: ask }
  - { tool: "mcp__gmail__*", decision: ask, started-by: [routine, event] }
  - { class: irreversible, decision: ask }
```

`tool` and `target` are patterns where `*` matches anything. The target of a shell command is its program, or the program and its arguments when the pattern has a space. For file tools it is the path, for fetches the host, and for MCP tools the tool name. `class` is `outward`, `irreversible` or `security`. `started-by` limits a rule to work started that way (`you`, `routine`, `check-back`, `event`, `handoff`). `decision` is `allow`, `ask` or `deny`; `expires` (an RFC 3339 time) and `note` are optional. When several rules match, deny wins over ask, and ask wins over allow. No rule turns an unattended outward action into anything but a draft.

`agents.execution` sets what the node allows without a rule: `ask` (the default), `allow` or `never-unattended`. With `never-unattended`, unattended work can't run any action.

### Policy hook

`agents.approvals.hook` names a program (`command`) or an HTTPS address (`url`) that Argus asks about every agent action. A program reads the request on standard input and writes the reply on standard output; an address gets the request as a JSON POST. The request is:

```json
{"action": {"tool": "Bash", "target": "git", "class": ["outward"], "input": {"command": "git push"}},
 "agent": {"id": "...", "name": "scout"}, "thread": "...", "started_by": "routine", "session": "...", "node": "..."}
```

The reply is `{"decision": "allow" | "ask" | "deny", "reason": "..."}`. `mode` sets what the reply does:

- `shadow` (the default): the reply is only recorded.
- `fail-open`: the reply decides; if the hook fails or takes longer than `timeout` (5s), the rules decide.
- `fail-closed`: the reply decides; if the hook fails or times out, the action is denied.

The hook can't overrule drafts, or the denial of an action you already denied.

### Activity

Every decision is recorded:

```sh
argus agent activity scout
argus agent activity scout --unattended
```
