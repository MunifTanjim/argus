/// The display name of a session's agent; unknown agents show their id.
String agentLabel(String agent) {
  switch (agent) {
    case 'claude':
      return 'Claude';
    case 'codex':
      return 'Codex';
    case 'antigravity':
      return 'Antigravity';
    case 'opencode':
      return 'OpenCode';
    case 'pi':
      return 'Pi';
    default:
      return agent;
  }
}
