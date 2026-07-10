import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/session_card.dart';

Session _session(String json) =>
    Session.fromJson(jsonDecode(json) as Map<String, dynamic>);

void main() {
  testWidgets('renders glyph, repo, task and meta; fires onTap', (tester) async {
    var tapped = false;
    final s = _session(
        '{"id":"mac:%1","agent":"claude","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus","summary":{"model_name":"Opus 4.8","model_color":"#d3869b","has_context":true,"context_pct":42.5,"tokens":12300,"task":"fix the bug"},"node_label":"mac"}');

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: SessionCard(session: s, onTap: () => tapped = true)),
    ));

    expect(find.text('●'), findsOneWidget);
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('fix the bug'), findsOneWidget);
    expect(find.textContaining('43%'), findsOneWidget); // 42.5 rounds to 43

    await tester.tap(find.byType(SessionCard));
    expect(tapped, isTrue);
  });

  testWidgets('shows agent badge when showAgent is set', (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"codex","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"}');
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: SessionCard(session: s, showAgent: true)),
    ));
    expect(find.text('CODEX'), findsOneWidget);
  });

  testWidgets('no agent badge by default', (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"codex","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"}');
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: SessionCard(session: s)),
    ));
    expect(find.text('CODEX'), findsNothing);
  });

  testWidgets('shows the session name beside the repo', (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"claude","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus","name":"auth-refactor"}');
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: SessionCard(session: s))));
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('auth-refactor'), findsOneWidget);
  });

  testWidgets('omits the name line when it duplicates the title', (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"claude","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"name":"auth-refactor"}');
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: SessionCard(session: s))));
    // No repo, so name is the title — it appears exactly once (not twice).
    expect(find.text('auth-refactor'), findsOneWidget);
  });

  testWidgets('falls back to id when no repo/name', (tester) async {
    final s = _session(
        '{"id":"mac:%9","agent":"t","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%9","session_name":"s","window_index":0,"current_path":"/p"}}');
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: SessionCard(session: s))));
    expect(find.text('mac:%9'), findsOneWidget);
  });
}
