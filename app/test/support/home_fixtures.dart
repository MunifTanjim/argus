import 'dart:convert';

import 'package:argus/models/session.dart';

Session _s(
  String id,
  String status,
  String node,
  String repo,
  String task, {
  Map<String, dynamic>? interaction,
}) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': id,
          'agent': 'claude',
          'status': status,
          'source': 'hooked',
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 's',
            'window_index': 0,
            'current_path': '/p',
          },
          'repo': repo,
          'node_id': node,
          'node_label': node,
          'summary': {
            'model_name': 'Opus 4.8',
            'model_color': '#d3869b',
            'has_context': true,
            'context_pct': 42.5,
            'tokens': 12300,
            'task': task,
          },
          'interaction': ?interaction,
        }),
      )
      as Map<String, dynamic>,
);

/// The sessions the home goldens show: one blocked on a permission prompt,
/// one waiting, two working, one idle, across two hosts.
final homeFixtureSessions = [
  _s(
    'mac:%1',
    'awaiting_input',
    'mac',
    'argus',
    'run the tests',
    interaction: {
      'kind': 'permission',
      'tool_name': 'Bash',
      'tool_input': '{"command":"flutter test"}',
    },
  ),
  _s(
    'mac:%2',
    'awaiting_input',
    'mac',
    'website',
    'finish the landing page',
    interaction: {'kind': 'idle'},
  ),
  _s('mac:%3', 'working', 'mac', 'argus', 'fix the bug'),
  _s('linux:%4', 'working', 'linux', 'infra', 'migrate the database'),
  _s('linux:%5', 'idle', 'linux', 'infra', 'review the diff'),
];
