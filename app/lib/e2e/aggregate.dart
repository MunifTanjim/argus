/// Client-side aggregation primitives — a Dart port of Go internal/client/aggregate.go.
/// Reproduces the gateway's node-qualified addressing so a blind gateway can relay
/// while the client merges/routes across nodes.

/// Methods carrying a composite session_id the client splits and routes to a node.
const Set<String> sessionAddressed = {
  'sessions.transcriptView', 'sessions.toolDetail', 'sessions.capture', 'sessions.input',
  'sessions.key', 'sessions.respond', 'sessions.kill', 'sessions.changedFiles',
  'sessions.fileDiff', 'sessions.commits', 'sessions.commitFiles', 'sessions.focus',
  'transcript.subscribe', 'terminal.open',
};

/// Methods routed by an explicit node_id (or the sole node).
const Set<String> nodeAddressed = {
  'sessions.spawn', 'sessions.resume', 'agents.list', 'sessions.exportBundle',
  'sessions.historySessions', 'sessions.historyTranscript', 'sessions.historyToolDetail',
};

/// Methods carrying a term_id, routed to the node the terminal was opened on.
const Set<String> terminalHandleAddressed = {
  'terminal.input', 'terminal.resize', 'terminal.close',
};

/// Methods whose result carries a node-local session_id that must be composited.
const Set<String> compositeResultMethods = {'sessions.spawn', 'sessions.resume'};

/// Joins a node id and node-local id into a gateway composite id.
String compositeId(String nodeId, String id) => '$nodeId:$id';

/// Splits a composite id on the FIRST ':'. ok is false when there is no ':'.
(String, String, bool) splitCompositeId(String s) {
  final i = s.indexOf(':');
  if (i < 0) return ('', s, false);
  return (s.substring(0, i), s.substring(i + 1), true);
}

/// Stamps a session map with its node origin + composite id and clears offline.
Map<String, dynamic> withOriginJson(Map<String, dynamic> s, String nodeId, String? label) {
  final id = s['id'];
  return {
    ...s,
    'id': compositeId(nodeId, id is String ? id : ''),
    'node_id': nodeId,
    'node_label': label,
    'offline': false,
  };
}

/// Returns a copy of params with only session_id replaced.
Map<String, dynamic> rewriteSessionId(Object? params, String id) {
  final m = <String, dynamic>{};
  if (params is Map) m.addAll(params.cast<String, dynamic>());
  m['session_id'] = id;
  return m;
}

/// Reads a String field from a params map, or null.
String? stringField(Object? params, String field) {
  if (params is Map) {
    final v = params[field];
    if (v is String) return v;
  }
  return null;
}
