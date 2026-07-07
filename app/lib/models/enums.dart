enum SessionStatus { discovered, working, awaitingInput, idle, dead, unknown }

SessionStatus statusFromWire(String? s) {
  switch (s) {
    case 'discovered':
      return SessionStatus.discovered;
    case 'working':
      return SessionStatus.working;
    case 'awaiting_input':
      return SessionStatus.awaitingInput;
    case 'idle':
      return SessionStatus.idle;
    case 'dead':
      return SessionStatus.dead;
    default:
      return SessionStatus.unknown;
  }
}

enum SessionSource { discovered, spawned, hooked, unknown }

SessionSource sourceFromWire(String? s) {
  switch (s) {
    case 'discovered':
      return SessionSource.discovered;
    case 'spawned':
      return SessionSource.spawned;
    case 'hooked':
      return SessionSource.hooked;
    default:
      return SessionSource.unknown;
  }
}

enum TmuxServerKind { default_, argus, unknown }

TmuxServerKind tmuxServerFromWire(String? s) {
  switch (s) {
    case 'default':
      return TmuxServerKind.default_;
    case 'argus':
      return TmuxServerKind.argus;
    default:
      return TmuxServerKind.unknown;
  }
}

enum InteractionKind { permission, question, plan, idle, unknown }

InteractionKind interactionKindFromWire(String? s) {
  switch (s) {
    case 'permission':
      return InteractionKind.permission;
    case 'question':
      return InteractionKind.question;
    case 'plan':
      return InteractionKind.plan;
    case 'idle':
      return InteractionKind.idle;
    default:
      return InteractionKind.unknown;
  }
}

enum TerminalExitReason { exited, evicted }

// Absent/unknown reasons mean the attach simply ended, so default to exited
// (mirrors the node's empty-reason == process-exit contract).
TerminalExitReason terminalExitReasonFromWire(String? s) {
  switch (s) {
    case 'evicted':
      return TerminalExitReason.evicted;
    default:
      return TerminalExitReason.exited;
  }
}

enum FrontendKind { tmux, vscode, external, unknown }

FrontendKind frontendFromWire(String? s) {
  switch (s) {
    case 'tmux':
      return FrontendKind.tmux;
    case 'vscode':
      return FrontendKind.vscode;
    case 'external':
      return FrontendKind.external;
    default:
      return FrontendKind.unknown;
  }
}
