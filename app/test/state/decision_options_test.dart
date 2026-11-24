import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import 'package:argus/models/session.dart';
import 'package:argus/state/decision_options.dart';

Session _session(Map<String, dynamic> interaction) => Session.fromJson(
  jsonDecode(
    jsonEncode({
      'id': 's1',
      'agent': 't',
      'status': 'waiting',
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'interaction': interaction,
    }),
  ),
);

void main() {
  test('effectiveOptions falls back to Allow and Deny', () {
    final s = _session({'kind': 'permission'});
    final o = effectiveOptions(s.interaction!);
    expect(o.map((e) => (e.label, e.value, e.reject)), [
      ('Allow', 'allow', false),
      ('Deny', 'deny', true),
    ]);
  });

  test('effectiveOptions keeps server options in order', () {
    final s = _session({
      'kind': 'permission',
      'options': [
        {'label': 'Yes', 'value': 'y'},
        {'label': 'Always', 'value': 'a'},
        {'label': 'No', 'value': 'n', 'reject': true},
      ],
    });
    expect(effectiveOptions(s.interaction!).map((e) => e.value), [
      'y',
      'a',
      'n',
    ]);
  });

  test('decisionKind maps permission and plan', () {
    expect(
      decisionKind(_session({'kind': 'permission'}).interaction!),
      'permission',
    );
    expect(decisionKind(_session({'kind': 'plan'}).interaction!), 'plan');
  });

  test('oneTapRespond matches the panel request', () {
    final s = _session({'kind': 'permission', 'request_id': 'r1'});
    final allow = effectiveOptions(s.interaction!).first;
    expect(oneTapRespond(s, allow), {
      'session_id': 's1',
      'kind': 'permission',
      'request_id': 'r1',
      'option_value': 'allow',
    });
  });
}
