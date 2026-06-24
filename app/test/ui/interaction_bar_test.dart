// app/test/ui/interaction_bar_test.dart
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/interaction_bar.dart';

Interaction _ix(Map<String, dynamic> j) =>
    Interaction.fromJson(jsonDecode(jsonEncode(j)) as Map<String, dynamic>);

void main() {
  test('label by kind', () {
    expect(interactionLabel(_ix({'kind': 'permission', 'tool_name': 'Bash'})),
        'Permission: Bash');
    expect(interactionLabel(_ix({'kind': 'plan'})), 'Plan review');
    expect(interactionLabel(_ix({'kind': 'question'})), 'Question');
    expect(interactionLabel(_ix({'kind': 'idle'})), 'Reply');
  });

  testWidgets('tapping bar invokes onRespond', (tester) async {
    var tapped = false;
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: InteractionBar(
          interaction: _ix({'kind': 'permission', 'tool_name': 'Bash'}),
          onRespond: () => tapped = true,
        ),
      ),
    ));
    await tester.tap(find.text('Respond'));
    expect(tapped, isTrue);
  });
}
