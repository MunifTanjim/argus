import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/agent_badge.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/theme.dart';

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
    expect(find.textContaining('12.3k tok'), findsOneWidget);

    await tester.tap(find.byType(SessionCard));
    expect(tapped, isTrue);
  });

  testWidgets('filled: shows agent badge when showAgent is set', (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"codex","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"}');
    await tester.pumpWidget(MaterialApp(
      theme: buildArgusTheme(
        prefs: const AppearancePrefs(statusStyle: StatusStyle.filled),
      ),
      home: Scaffold(body: SessionCard(session: s, showAgent: true)),
    ));
    expect(find.text('CODEX'), findsOneWidget);
  });

  for (final style in [StatusStyle.stripe, StatusStyle.dot]) {
    testWidgets('$style: the meta line names the agent, with no badge', (
      tester,
    ) async {
      final s = _session(
          '{"id":"mac:%1","agent":"codex","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"}');
      await tester.pumpWidget(MaterialApp(
        theme: buildArgusTheme(prefs: AppearancePrefs(statusStyle: style)),
        home: Scaffold(body: SessionCard(session: s, showAgent: true)),
      ));
      expect(find.byType(AgentBadge), findsNothing);
      expect(find.textContaining(agentLabel('codex')), findsOneWidget);
    });
  }

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

  Widget app(Session s, StatusStyle style) => MaterialApp(
        theme: buildArgusTheme(prefs: AppearancePrefs(statusStyle: style)),
        home: Scaffold(body: SessionCard(session: s)),
      );

  const base =
      '"agent":"claude","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"';

  testWidgets('stripe style draws a stripe in the status color', (tester) async {
    final s = _session('{"id":"a",$base,"status":"working"}');
    await tester.pumpWidget(app(s, StatusStyle.stripe));
    final stripe = tester.widget<Container>(find.byKey(const Key('status-stripe')));
    final t = tester.element(find.byType(SessionCard)).tokens;
    expect(stripe.color, t.working);
    expect(find.byType(Card), findsNothing);
  });

  testWidgets('dot style draws the status glyph and no stripe', (tester) async {
    final s = _session('{"id":"a",$base,"status":"working"}');
    await tester.pumpWidget(app(s, StatusStyle.dot));
    expect(find.text('●'), findsOneWidget);
    expect(find.byKey(const Key('status-stripe')), findsNothing);
  });

  testWidgets('filled style keeps the tinted card', (tester) async {
    final s = _session('{"id":"a",$base,"status":"awaiting_input"}');
    await tester.pumpWidget(app(s, StatusStyle.filled));
    final t = tester.element(find.byType(SessionCard)).tokens;
    final card = tester.widget<Material>(find
        .descendant(of: find.byType(SessionCard), matching: find.byType(Material))
        .first);
    expect(card.color, t.waitingSurface);
    expect((card.shape! as RoundedRectangleBorder).side.color, t.waitingBorder);
    expect(find.byKey(const Key('status-stripe')), findsNothing);
  });

  testWidgets('idle session title is dim', (tester) async {
    final s = _session('{"id":"a",$base,"status":"idle"}');
    await tester.pumpWidget(app(s, StatusStyle.stripe));
    final t = tester.element(find.byType(SessionCard)).tokens;
    expect(tester.widget<Text>(find.text('argus')).style!.color, t.textDim);
  });

  testWidgets('filled card takes the corner radius from the theme',
      (tester) async {
    final s = _session(
        '{"id":"mac:%1","agent":"claude","status":"idle","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"argus"}');
    const prefs = AppearancePrefs(
      statusStyle: StatusStyle.filled,
      radius: CornerRadius.rounded,
    );
    await tester.pumpWidget(MaterialApp(
      theme: buildArgusTheme(prefs: prefs),
      home: Scaffold(body: SessionCard(session: s, onTap: () {})),
    ));
    final card = tester.widget<Material>(find
        .descendant(of: find.byType(SessionCard), matching: find.byType(Material))
        .first);
    expect((card.shape! as RoundedRectangleBorder).borderRadius,
        BorderRadius.circular(12));
    expect(card.clipBehavior, Clip.antiAlias);
  });

  TextSpan spanWith(WidgetTester tester, String text) {
    for (final rt in tester.widgetList<RichText>(find.byType(RichText))) {
      TextSpan? hit;
      rt.text.visitChildren((span) {
        if (span is TextSpan && span.text == text) {
          hit = span;
          return false;
        }
        return true;
      });
      if (hit != null) return hit!;
    }
    throw StateError('no span "$text"');
  }

  for (final style in StatusStyle.values) {
    testWidgets('${style.name}: name is bright, model and context colored',
        (tester) async {
      final s = _session('{"id":"mac:%1","status":"working",$base,'
          '"name":"auth-refactor","summary":{"model_name":"Opus 4.8",'
          '"model_color":"#d3869b","has_context":true,"context_pct":85}}');
      await tester.pumpWidget(app(s, style));
      final t = tester.element(find.byType(SessionCard)).tokens;

      expect(tester.widget<Text>(find.text('auth-refactor')).style?.color,
          t.text);
      expect(spanWith(tester, 'Opus 4.8').style?.color,
          const Color(0xFFd3869b));
      expect(spanWith(tester, '85% ctx').style?.color, t.error);
    });
  }

  testWidgets('context color follows the TUI thresholds', (tester) async {
    for (final (pct, pick) in [
      (42, (ArgusTokens t) => t.success),
      (60, (ArgusTokens t) => t.warning),
    ]) {
      final s = _session('{"id":"mac:%1","status":"working",$base,'
          '"summary":{"has_context":true,"context_pct":$pct}}');
      await tester.pumpWidget(app(s, StatusStyle.stripe));
      final t = tester.element(find.byType(SessionCard)).tokens;
      expect(spanWith(tester, '$pct% ctx').style?.color, pick(t));
    }
  });

  testWidgets('an unreadable model color falls back to text', (tester) async {
    final s = _session('{"id":"mac:%1","status":"working",$base,'
        '"summary":{"model_name":"M","model_color":"#ebdbb2"}}');
    await tester.pumpWidget(MaterialApp(
      theme: buildArgusTheme(palette: nordLight),
      home: Scaffold(body: SessionCard(session: s)),
    ));
    final t = tester.element(find.byType(SessionCard)).tokens;
    expect(spanWith(tester, 'M').style?.color, t.text);
  });

  for (final style in StatusStyle.values) {
    testWidgets('${style.name}: session name is the title, repo above it',
        (tester) async {
      final s = _session('{"id":"mac:%1","status":"working",$base,'
          '"name":"auth-refactor"}');
      await tester.pumpWidget(app(s, style));
      final t = tester.element(find.byType(SessionCard)).tokens;
      final name = tester.widget<Text>(find.text('auth-refactor')).style!;
      final repo = tester.widget<Text>(find.text('argus')).style!;
      expect(name.color, t.text);
      expect(repo.color, t.textDim);
      expect(repo.fontSize!, lessThan(name.fontSize!));
      expect(tester.getTopLeft(find.text('argus')).dy,
          lessThan(tester.getTopLeft(find.text('auth-refactor')).dy));
    });
  }

  testWidgets('without a name the repo is the title', (tester) async {
    final s = _session('{"id":"mac:%1","status":"working",$base}');
    await tester.pumpWidget(app(s, StatusStyle.stripe));
    final t = tester.element(find.byType(SessionCard)).tokens;
    expect(tester.widget<Text>(find.text('argus')).style!.color, t.text);
  });

  Material rowMaterial(WidgetTester tester) => tester.widget<Material>(
      find.descendant(
              of: find.byType(SessionCard), matching: find.byType(Material))
          .first);

  for (final style in [StatusStyle.stripe, StatusStyle.dot]) {
    testWidgets('${style.name} row sits on the surface color', (tester) async {
      final s = _session('{"id":"mac:%1","status":"working",$base}');
      await tester.pumpWidget(
        MaterialApp(
          theme: buildArgusTheme(prefs: AppearancePrefs(statusStyle: style)),
          home: Scaffold(body: SessionCard(session: s)),
        ),
      );
      final t = tester.element(find.byType(SessionCard)).tokens;
      final m = rowMaterial(tester);
      expect(m.color, t.surface);
      expect(m.color, isNot(t.canvas));
      expect(m.borderRadius, BorderRadius.circular(t.radius));
    });
  }

  for (final style in StatusStyle.values) {
    testWidgets('${style.name}: mark and node on the first line, '
        'name at the left', (tester) async {
      final s = _session('{"id":"mac:%1","status":"working",$base,'
          '"name":"auth-refactor","node_label":"mac",'
          '"summary":{"task":"fix it"}}');
      await tester.pumpWidget(MaterialApp(
        theme: buildArgusTheme(prefs: AppearancePrefs(statusStyle: style)),
        home: Scaffold(body: SessionCard(session: s, showNode: true)),
      ));
      final mark = find.text('●');
      final repoY = tester.getCenter(find.text('argus')).dy;
      expect(tester.getCenter(mark).dy, closeTo(repoY, 1));
      expect(tester.getCenter(find.text('mac')).dy, closeTo(repoY, 1));
      expect(tester.getTopLeft(find.text('auth-refactor')).dx,
          tester.getTopLeft(find.text('fix it')).dx);
      expect(tester.getTopLeft(mark).dx,
          closeTo(tester.getTopLeft(find.text('fix it')).dx, 5));
    });
  }

  testWidgets('without a name the mark is centered on the title',
      (tester) async {
    final s = _session('{"id":"mac:%1","status":"working",$base,'
        '"node_label":"mac"}');
    await tester.pumpWidget(MaterialApp(
      theme: buildArgusTheme(),
      home: Scaffold(body: SessionCard(session: s, showNode: true)),
    ));
    expect(tester.getCenter(find.text('●')).dy,
        closeTo(tester.getCenter(find.text('argus')).dy, 1));
    expect(tester.getCenter(find.text('mac')).dy,
        closeTo(tester.getCenter(find.text('argus')).dy, 1));
  });
}
