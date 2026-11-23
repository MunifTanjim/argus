import 'package:argus/models/entry.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/transcript_feed.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

Widget _feed(List<Entry> entries) => ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: MaterialApp(
        home: Scaffold(
          body: TranscriptFeed(
            detailRef: const ToolDetailRef.live('s'),
            entries: entries,
          ),
        ),
      ),
    );

const _link = Entry(
  id: 'l',
  kind: EntryKind.text,
  text: 'see [docs](https://argus.muniftanjim.dev)',
);

void main() {
  testWidgets('the transcript feed has a SelectionArea', (tester) async {
    await tester.pumpWidget(_feed(const [_link]));
    expect(
      find.ancestor(
        of: find.byType(SuperListView),
        matching: find.byType(SelectionArea),
      ),
      findsOneWidget,
    );
    // Markdown rows add no nested area of their own.
    expect(find.byType(SelectionArea), findsOneWidget);
  });

  testWidgets(
    'a tap on a collapsible entry row inside the SelectionArea still toggles it',
    (tester) async {
      final long = List.generate(30, (i) => 'line $i').join('\n');
      await tester.pumpWidget(_feed([
        Entry(id: 'u', kind: EntryKind.user, text: long),
      ]));
      await tester.pumpAndSettle();
      expect(find.textContaining('line 29'), findsNothing);
      await tester.tap(find.text('Show more'));
      await tester.pumpAndSettle();
      expect(find.text('Show less'), findsOneWidget);
      expect(find.textContaining('line 29'), findsOneWidget);
    },
  );

  testWidgets('a tap on a link inside the transcript still opens the link actions',
      (tester) async {
    await tester.pumpWidget(_feed(const [_link]));
    await tester.pumpAndSettle();
    await tester.tap(find.text('docs'));
    await tester.pumpAndSettle();
    expect(find.text('Open link'), findsOneWidget);
    expect(find.text('Copy link'), findsOneWidget);
  });

  testWidgets('a drag across two rows selects text', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    String? copied;
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        copied = (call.arguments as Map)['text'] as String;
      }
      return null;
    });
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, null));

    await tester.pumpWidget(_feed(const [
      Entry(id: 'a', kind: EntryKind.text, text: 'alpha first row'),
      Entry(id: 'b', kind: EntryKind.text, text: 'beta second row'),
    ]));
    await tester.pumpAndSettle();

    final from = tester.getTopLeft(find.textContaining('alpha')) +
        const Offset(1, 4);
    final to = tester.getBottomRight(find.textContaining('beta')) -
        const Offset(1, 4);
    final g = await tester.startGesture(from, kind: PointerDeviceKind.mouse);
    await tester.pump();
    await g.moveTo(to);
    await tester.pump();
    await g.up();
    await tester.pumpAndSettle();

    await tester.sendKeyDownEvent(LogicalKeyboardKey.controlLeft);
    await tester.sendKeyEvent(LogicalKeyboardKey.keyC);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.controlLeft);
    await tester.pumpAndSettle();

    expect(copied, contains('alpha first row'));
    expect(copied, contains('beta second row'));
    debugDefaultTargetPlatformOverride = null;
  });

  testWidgets(
    'a right-click on a link shows the link actions and no selection toolbar',
    (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.linux;
      await tester.pumpWidget(_feed(const [_link]));
      await tester.pumpAndSettle();
      final g = await tester.startGesture(
        tester.getCenter(find.text('docs')),
        kind: PointerDeviceKind.mouse,
        buttons: kSecondaryMouseButton,
      );
      await g.up();
      await tester.pumpAndSettle();
      expect(find.text('Open link'), findsOneWidget);
      expect(find.byType(AdaptiveTextSelectionToolbar), findsNothing);
      debugDefaultTargetPlatformOverride = null;
    },
  );

  testWidgets('a right-click on plain text still shows the selection toolbar',
      (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.linux;
    await tester.pumpWidget(_feed(const [_link]));
    await tester.pumpAndSettle();
    final g = await tester.startGesture(
      tester.getTopLeft(find.textContaining('see')) + const Offset(3, 6),
      kind: PointerDeviceKind.mouse,
      buttons: kSecondaryMouseButton,
    );
    await g.up();
    await tester.pumpAndSettle();
    expect(find.byType(AdaptiveTextSelectionToolbar), findsOneWidget);
    expect(find.text('Open link'), findsNothing);
    debugDefaultTargetPlatformOverride = null;
  });
}
