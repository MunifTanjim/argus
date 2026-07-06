import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/agent_badge.dart';

void main() {
  test('agentLabel maps known ids and passes through unknown', () {
    expect(agentLabel('claude'), 'Claude');
    expect(agentLabel('codex'), 'Codex');
    expect(agentLabel('antigravity'), 'Antigravity');
    expect(agentLabel('future'), 'future');
    expect(agentLabel(''), '');
  });

  testWidgets('AgentBadge renders label text', (tester) async {
    await tester.pumpWidget(const MaterialApp(
      home: Scaffold(body: AgentBadge(agent: 'codex')),
    ));
    expect(find.text('CODEX'), findsOneWidget);
  });

  testWidgets('AgentBadge is empty for blank agent', (tester) async {
    await tester.pumpWidget(const MaterialApp(
      home: Scaffold(body: AgentBadge(agent: '')),
    ));
    expect(find.byType(Text), findsNothing);
  });
}
