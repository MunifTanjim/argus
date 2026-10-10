import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/control.dart';
import 'package:argus/ui/unsupported_server_screen.dart';

void main() {
  Future<int Function()> pumpWithServer(WidgetTester tester, String version) async {
    var fetches = 0;
    await tester.pumpWidget(ProviderScope(
      overrides: [
        serverInfoProvider.overrideWith((ref) async {
          fetches++;
          return ServerInfo(version: version, nodes: const []);
        }),
      ],
      child: const MaterialApp(home: ServerGate(child: Text('app'))),
    ));
    await tester.pump();
    return () => fetches;
  }

  testWidgets('an old server blocks the app with an upgrade notice', (tester) async {
    await pumpWithServer(tester, '0.1.1');
    expect(find.textContaining('Upgrade argus'), findsOneWidget);
    expect(find.textContaining('0.1.1'), findsOneWidget);
    expect(find.text('app'), findsNothing);
  });

  testWidgets('Retry fetches the server version again', (tester) async {
    final fetches = await pumpWithServer(tester, '0.1.1');
    final before = fetches();
    await tester.tap(find.text('Retry'));
    await tester.pump();
    expect(fetches(), before + 1);
  });

  testWidgets('a supported or unknown server shows the app', (tester) async {
    await pumpWithServer(tester, 'dev');
    expect(find.text('app'), findsOneWidget);
  });
}
