import 'package:argus/ui/edit_diff.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Widget _wrap(String diff) =>
    MaterialApp(home: Scaffold(body: unifiedDiffView(diff, lang: 'x.txt')));

void main() {
  testWidgets('shows hunk headers and new-side numbers from the header',
      (tester) async {
    await tester.pumpWidget(_wrap('--- a/x.txt\n+++ b/x.txt\n'
        '@@ -40,2 +40,3 @@ section\n'
        ' keep\n'
        '-old\n'
        '+new\n'
        '+more\n'));
    await tester.pumpAndSettle();

    expect(find.textContaining('@@ -40,2 +40,3 @@ section'), findsOneWidget);
    expect(find.textContaining('- old'), findsOneWidget);
    expect(find.textContaining('+ new'), findsOneWidget);
    expect(find.text('40'), findsOneWidget); // keep
    expect(find.text('41'), findsOneWidget); // new
    expect(find.text('42'), findsOneWidget); // more
    expect(find.textContaining('+++'), findsNothing);
  });

  testWidgets('shows the no-newline marker', (tester) async {
    await tester.pumpWidget(
        _wrap('@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n'));
    await tester.pumpAndSettle();
    expect(find.text(r'\ No newline at end of file'), findsOneWidget);
  });
}
