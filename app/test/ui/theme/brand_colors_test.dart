import 'package:argus/ui/agent_badge.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  const agents = ['claude', 'codex', 'antigravity', 'opencode', 'pi', 'x'];
  const teams = [
    'blue',
    'green',
    'red',
    'yellow',
    'purple',
    'cyan',
    'orange',
    'pink',
  ];

  for (final p in [gruvboxDark, nordLight]) {
    test('${p.name} ${p.brightness.name}: brand colors stay readable', () {
      final t = ArgusTokens.from(p);
      final brands = [
        for (final a in agents) agentBrandColor(a),
        for (final n in teams) teamBrandColor(n, t),
      ];
      for (final c in brands) {
        final got = readableOn(c, t);
        if (contrastRatio(c, t.canvas) >= 3.0) {
          expect(got, c);
        } else {
          expect(got, t.text);
        }
      }
    });
  }

  test('gruvbox dark keeps every agent brand color', () {
    final t = ArgusTokens.from(gruvboxDark);
    for (final a in agents) {
      expect(agentColor(a, t), agentBrandColor(a));
    }
  });

  test('nord light swaps the opencode color for text', () {
    final t = ArgusTokens.from(nordLight);
    expect(agentColor('opencode', t), t.text);
    expect(readableOn(teamBrandColor('pink', t), t), t.text);
  });

  testWidgets('agent badge label is readable on a light palette', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildArgusTheme(palette: nordLight),
        home: const Scaffold(body: AgentBadge(agent: 'opencode')),
      ),
    );
    final text = tester.widget<Text>(find.text('OPENCODE'));
    expect(text.style?.color, ArgusTokens.from(nordLight).text);
  });
}
