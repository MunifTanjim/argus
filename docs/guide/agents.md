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
