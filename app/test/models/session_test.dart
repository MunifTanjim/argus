import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/registry_event.dart';

const _sessionJson = '''
{
  "id":"macbook:%3","agent":"claude","status":"awaiting_input","source":"discovered",
  "tmux":{"server":"default","pane_id":"%3","session_name":"argus","window_index":0,"current_path":"/home/u/argus"},
  "repo":"argus",
  "summary":{"model":"claude-opus-4-8","has_context":true,"context_pct":42.5,"tokens":12300,"task":"fix bug","last_activity":"2026-06-20T10:00:00Z"},
  "interaction":{"kind":"permission","tool_name":"bash","tool_input":"go test ./..."},
  "node_id":"macbook","node_label":"MacBook"
}
''';

void main() {
  test('parses a full session', () {
    final s = Session.fromJson(jsonDecode(_sessionJson));
    expect(s.id, 'macbook:%3');
    expect(s.status, SessionStatus.awaitingInput);
    expect(s.repo, 'argus');
    expect(s.tmux.paneId, '%3');
    expect(s.tmux.server, TmuxServerKind.default_);
    expect(s.summary!.contextPct, 42.5);
    expect(s.summary!.tokens, 12300);
    expect(s.interaction!.kind, InteractionKind.permission);
    expect(s.interaction!.toolName, 'bash');
    expect(s.nodeLabel, 'MacBook');
    expect(s.offline, isFalse);
  });

  test('parses a minimal session (omitted optionals)', () {
    final s = Session.fromJson(jsonDecode(
        '{"id":"x:%1","agent":"claude","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":1,"current_path":"/p"}}'));
    expect(s.summary, isNull);
    expect(s.interaction, isNull);
    expect(s.repo, isNull);
    expect(s.tmux.server, TmuxServerKind.argus);
  });

  test('parses an AskUserQuestion interaction with options', () {
    final s = Session.fromJson(jsonDecode(
        '{"id":"x:%1","agent":"t","status":"awaiting_input","source":"hooked","tmux":{"server":"default","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"interaction":{"kind":"question","questions":[{"header":"DB","question":"Which db?","options":["pg","sqlite"],"option_descriptions":["relational","embedded"]}]}}'));
    final q = s.interaction!.questions.single;
    expect(q.header, 'DB');
    expect(q.options, ['pg', 'sqlite']);
    expect(q.optionDescriptions, ['relational', 'embedded']);
    expect(q.multiSelect, isFalse);
  });

  test('parses server status_label', () {
    final s = Session.fromJson({
      'id': 's1', 'agent': 'claude', 'status': 'working', 'source': 'hooked',
      'status_label': 'working',
      'tmux': {'server': 'default', 'pane_id': '%1'},
    });
    expect(s.statusLabel, 'working');
  });

  test('parses a registry event', () {
    final e = RegistryEvent.fromJson(
        jsonDecode('{"type":"updated","session":$_sessionJson}'));
    expect(e.type, RegistryEventType.updated);
    expect(e.session.id, 'macbook:%3');
  });

  test('parses vscode frontend and is not controllable', () {
    final s = Session.fromJson({
      'id': 'claude:vs1',
      'tmux': {'pane_id': ''},
      'status': 'idle',
      'source': 'hooked',
      'frontend': 'vscode',
    });
    expect(s.frontend, FrontendKind.vscode);
    expect(s.controllable, isFalse);
  });

  test('tmux session is controllable', () {
    final s = Session.fromJson({
      'id': 's1',
      'tmux': {'pane_id': '%3'},
      'status': 'idle',
      'source': 'discovered',
      'frontend': 'tmux',
    });
    expect(s.frontend, FrontendKind.tmux);
    expect(s.controllable, isTrue);
  });
}
