// Client-side aggregation primitives — a Dart port of Go internal/client/aggregate.go.
// Reproduces the gateway's node-qualified addressing so a blind gateway can relay
// while the client merges/routes across nodes.

/// RPC error code returned from push.test (and nodes) when the push target is
/// permanently gone (HTTP 404/410). Mirrors api.CodePushGone on the server side.
const pushGoneCode = 410;

/// Methods sent to every connected node channel. Each node holds its own device
/// store, so register/unregister/test must reach all of them. push.vapidKey stays
/// a gateway passthrough (the subscription is created once; nodes store the target).
const Set<String> pushFanoutMethods = {
  'push.register',
  'push.unregister',
  'push.test',
  'push.setPause',
};

/// Methods carrying a composite session_id the client splits and routes to a node.
const Set<String> sessionAddressed = {
  'sessions.transcriptView', 'sessions.toolDetail', 'sessions.capture', 'sessions.input',
  'sessions.key', 'sessions.respond', 'sessions.kill', 'sessions.changedFiles',
  'sessions.fileDiff', 'sessions.commits', 'sessions.commitFiles', 'sessions.focus',
  'sessions.tasks', 'transcript.subscribe', 'terminal.open',
};

/// Methods routed by an explicit node_id (or the sole node).
const Set<String> nodeAddressed = {
  'sessions.spawn', 'sessions.resume', 'agents.list', 'sessions.exportBundle',
  'sessions.historySessions', 'sessions.historyTranscript', 'sessions.historyToolDetail',
  'terminal.create', 'host.info', 'host.setWakelock',
};

/// Methods carrying a term_id, routed to the node the terminal was opened on.
const Set<String> terminalHandleAddressed = {
  'terminal.input', 'terminal.resize', 'terminal.close',
};

/// Methods carrying a composite terminal_id the client splits and routes to a
/// node. terminal.open joins them when it names a terminal_id.
const Set<String> terminalAddressed = {'terminal.kill', 'terminal.rename'};

/// Methods carrying a composite workspace_id the client splits and routes to a
/// node.
const Set<String> workspaceAddressed = {
  'workspace.changedFiles', 'workspace.diff', 'workspace.listDir',
  'workspace.readFile', 'workspace.commits', 'workspace.commitFiles',
  'workspace.remove', 'workspace.setTarget', 'workspace.runSetup',
  'workspace.setupLog',
};

/// Methods carrying a composite project_id the client splits and routes to a
/// node.
const Set<String> projectAddressed = {
  'workspace.create', 'project.rename', 'project.setHidden',
  'project.setPinned', 'project.forget', 'project.branches', 'project.prs',
  'project.issues',
};

/// Methods whose result carries a node-local session_id that must be composited.
const Set<String> compositeResultMethods = {'sessions.spawn', 'sessions.resume'};

String compositeId(String nodeId, String id) => '$nodeId:$id';

/// Splits a composite id on the FIRST ':'. ok is false when there is no ':'.
(String, String, bool) splitCompositeId(String s) {
  final i = s.indexOf(':');
  if (i < 0) return ('', s, false);
  return (s.substring(0, i), s.substring(i + 1), true);
}

Map<String, dynamic> withOriginJson(Map<String, dynamic> s, String nodeId, String? label) {
  final id = s['id'];
  final ws = s['workspace_id'];
  return {
    ...s,
    'id': compositeId(nodeId, id is String ? id : ''),
    if (ws is String && ws.isNotEmpty) 'workspace_id': compositeId(nodeId, ws),
    'node_id': nodeId,
    'node_label': label,
    'offline': false,
  };
}

/// Stamps a node-local project with its origin and composites its project and
/// workspace ids, so they match the composited session workspace_id.
Map<String, dynamic> projectWithOriginJson(
    Map<String, dynamic> p, String nodeId, String? label) {
  final ws = p['workspaces'];
  final id = p['id'];
  return {
    ...p,
    'id': compositeId(nodeId, id is String ? id : ''),
    'node_id': nodeId,
    'node_label': label,
    'workspaces': [
      if (ws is List)
        for (final w in ws)
          if (w is Map<String, dynamic>)
            {...w, 'id': compositeId(nodeId, w['id'] is String ? w['id'] as String : '')},
    ],
  };
}

Map<String, dynamic> terminalWithOriginJson(
    Map<String, dynamic> t, String nodeId, String? label) {
  final id = t['id'];
  return {
    ...t,
    'id': compositeId(nodeId, id is String ? id : ''),
    'node_id': nodeId,
    'node_label': label,
  };
}

/// Orders [items] by node [key], keeping each node's own order.
List<T> sortByNode<T>(List<T> items, String Function(T) key) {
  final indexed = [for (var i = 0; i < items.length; i++) (i, items[i])];
  indexed.sort((a, b) {
    final c = key(a.$2).compareTo(key(b.$2));
    return c != 0 ? c : a.$1.compareTo(b.$1);
  });
  return [for (final e in indexed) e.$2];
}

Map<String, dynamic> rewriteSessionId(Object? params, String id) {
  final m = <String, dynamic>{};
  if (params is Map) m.addAll(params.cast<String, dynamic>());
  m['session_id'] = id;
  return m;
}

String? stringField(Object? params, String field) {
  if (params is Map) {
    final v = params[field];
    if (v is String) return v;
  }
  return null;
}
