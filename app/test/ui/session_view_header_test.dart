import 'package:argus/models/session.dart';
import 'package:argus/ui/session_view_header.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s({
  String? name,
  String? branch,
  String frontend = 'tmux',
  Map<String, dynamic>? summary,
  String nodeLabel = 'mac',
}) =>
    Session.fromJson({
      'id': 'mac:%1',
      'agent': 'claude',
      'status': 'working',
      'source': 'hooked',
      'frontend': frontend,
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'repo': 'argus',
      'node_label': nodeLabel,
      'name': ?name,
      'branch': ?branch,
      'summary': ?summary,
    });

Future<void> _pump(WidgetTester t, Session s, {bool wrap = false}) =>
    t.pumpWidget(
      MaterialApp(
        home: Scaffold(body: SessionViewHeader(session: s, wrap: wrap)),
      ),
    );

void main() {
  testWidgets('named session: label, name, and meta line', (t) async {
    await _pump(
      t,
      _s(
        name: 'auth-refactor',
        branch: 'feat/auth',
        summary: {
          'model_name': 'Opus 4.8',
          'has_context': true,
          'context_pct': 43,
        },
      ),
    );
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('auth-refactor'), findsOneWidget);
    expect(find.text('mac'), findsOneWidget);
    expect(find.textContaining('⎇ feat/auth'), findsOneWidget);
    expect(find.textContaining('Opus 4.8'), findsOneWidget);
    expect(find.textContaining('43% ctx'), findsOneWidget);
  });

  testWidgets('unnamed session with no branch and no summary', (t) async {
    await _pump(t, _s());
    expect(find.text('argus'), findsOneWidget);
    expect(find.textContaining('⎇'), findsNothing);
    expect(find.textContaining(' · '), findsNothing);
  });

  testWidgets('a non-tmux frontend ends the meta line', (t) async {
    await _pump(t, _s(frontend: 'vscode', branch: 'main'));
    expect(find.textContaining('⎇ main · vscode'), findsOneWidget);
    expect(find.byType(Chip), findsNothing);
  });

  testWidgets('a long node label truncates on a narrow screen', (t) async {
    t.view.physicalSize = const Size(320, 600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await _pump(t, _s(nodeLabel: 'build-server-in-the-basement-rack-number-seven'));
    expect(t.takeException(), isNull);
  });

  testWidgets('the node name sits at the right edge with the node icon',
      (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await _pump(t, _s(name: 'auth-refactor'));
    expect(t.getRect(find.text('mac')).right, closeTo(390, 0.5));
    expect(find.byIcon(Icons.dns_outlined), findsOneWidget);
  });

  testWidgets('wrapped, a long branch shows whole on more lines', (t) async {
    t.view.physicalSize = const Size(320, 600);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    const branch = 'feat/a-very-long-branch-name-that-does-not-fit-on-one-line';
    await _pump(t, _s(name: 'auth-refactor', branch: branch), wrap: true);

    final meta = find.textContaining('⎇ $branch');
    expect(t.widget<Text>(meta).maxLines, isNull);
    expect(t.getSize(meta).height, greaterThan(20));
  });

  testWidgets('not wrapped, the meta line stays on one line', (t) async {
    await _pump(t, _s(branch: 'main'));
    expect(t.widget<Text>(find.textContaining('⎇ main')).maxLines, 1);
  });
}
