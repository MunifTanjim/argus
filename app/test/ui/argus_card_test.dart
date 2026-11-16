import 'package:argus/models/entry.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/entry_row.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Future<void> _pump(WidgetTester tester, Entry e) {
  var expanded = false;
  return tester.pumpWidget(
    ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) => SingleChildScrollView(
              child: EntryRow(
                detailRef: const ToolDetailRef.live('s'),
                entry: e,
                expanded: expanded,
                onToggle: () => setState(() => expanded = !expanded),
              ),
            ),
          ),
        ),
      ),
    ),
  );
}

const _reply = ArgusMessage(
  id: 'm-1a2b3c4d',
  kind: 'reply',
  from: 'worker-api',
  session: 'home:argus:%5',
  body: '**Purpose**\n\nAll tests pass.',
);

const _notice = ArgusMessage(
  id: 'm-5e6f7a8b',
  kind: 'interaction',
  from: 'argus',
  child: 'worker-db',
  childSession: 'home:argus:%7',
  body: 'Input: {\n  "cmd": "rm -rf build"\n}',
);

Entry _entry(List<ArgusMessage> msgs) => Entry(
  id: 'u',
  kind: EntryKind.user,
  timestamp: '1970-01-01T00:00:00Z',
  text: '<argus-message raw',
  argusMessages: msgs,
);

Color _borderOf(WidgetTester tester, Finder f) {
  final box = tester.widget<Container>(f).decoration! as BoxDecoration;
  return (box.border! as Border).top.color;
}

void main() {
  testWidgets('one argus message draws a card, not the user band', (
    tester,
  ) async {
    await _pump(tester, _entry(const [_reply]));
    expect(find.byKey(const ValueKey('argus-card')), findsOneWidget);
    for (final t in ['reply', 'worker-api', 'home:argus:%5', 'm-1a2b3c4d']) {
      expect(find.text(t), findsOneWidget, reason: t);
    }
    expect(find.textContaining('All tests pass.'), findsWidgets);
    expect(find.textContaining('**Purpose**'), findsNothing);
    expect(find.text('You'), findsNothing);
    expect(find.byKey(const ValueKey('user-band')), findsNothing);
    expect(find.text('argus · 1 messages'), findsNothing);
  });

  testWidgets('two messages show a count line and kind-colored borders', (
    tester,
  ) async {
    await _pump(tester, _entry(const [_reply, _notice]));
    expect(find.text('argus · 2 messages'), findsOneWidget);
    final frames = find.byWidgetPredicate(
      (w) =>
          w.key is ValueKey<String> &&
          (w.key! as ValueKey<String>).value.startsWith('argus-message:'),
    );
    expect(frames, findsNWidgets(2));
    expect(_borderOf(tester, frames.at(0)), AppColors.accent);
    expect(_borderOf(tester, frames.at(1)), AppColors.notice);
    expect(find.text('prompt'), findsOneWidget);
    expect(find.text('worker-db'), findsOneWidget);
    expect(find.text('home:argus:%7'), findsOneWidget);
  });

  testWidgets('a notice body keeps its lines as plain text', (tester) async {
    await _pump(tester, _entry(const [_notice]));
    final body = tester.widget<Text>(find.textContaining('"cmd"'));
    expect(body.data, contains('\n  "cmd": "rm -rf build"\n'));
  });

  testWidgets('an entry without argus messages keeps the user band', (
    tester,
  ) async {
    await _pump(tester, _entry(const []));
    expect(find.byKey(const ValueKey('user-band')), findsOneWidget);
    expect(find.text('You'), findsOneWidget);
    expect(find.byKey(const ValueKey('argus-card')), findsNothing);
  });

  testWidgets('a long body folds behind Show more / Show less', (tester) async {
    final long = List.generate(60, (i) => 'line $i').join('\n');
    await _pump(
      tester,
      _entry([ArgusMessage(id: 'm-1', kind: 'reply', from: 'w', body: long)]),
    );
    expect(find.text('Show more'), findsOneWidget);
    expect(find.textContaining('line 59'), findsNothing);
    await tester.tap(find.text('Show more'));
    await tester.pump();
    expect(find.text('Show less'), findsOneWidget);
    expect(find.textContaining('line 59'), findsWidgets);
  });

  testWidgets('a long notice stays monospace while folded', (tester) async {
    final input = List.generate(20, (i) => '  "k$i": "v"').join('\n');
    await _pump(
      tester,
      _entry([
        ArgusMessage(
          id: 'm-1',
          kind: 'interaction',
          from: 'argus',
          child: 'w',
          body: 'Input: {\n$input\n}',
        ),
      ]),
    );
    expect(find.text('Show more'), findsOneWidget);
    final body = tester.widget<Text>(find.textContaining('"k0"'));
    expect(body.style?.fontFamily, 'monospace');
  });

  testWidgets('an interaction_ended message is labeled prompt ended', (
    tester,
  ) async {
    await _pump(
      tester,
      _entry(const [
        ArgusMessage(
          id: 'm-1',
          kind: 'interaction_ended',
          from: 'argus',
          body: 'x',
        ),
      ]),
    );
    expect(find.text('prompt ended'), findsOneWidget);
  });

  testWidgets('unknown kind shows the raw kind', (tester) async {
    await _pump(
      tester,
      _entry(const [
        ArgusMessage(
          id: 'm-1',
          kind: 'prompt_notice',
          from: 'argus',
          body: 'x',
        ),
      ]),
    );
    expect(find.text('prompt_notice'), findsOneWidget);
  });
}
