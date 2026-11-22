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
