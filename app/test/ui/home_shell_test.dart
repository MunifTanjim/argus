import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/ui/home_shell.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  testWidgets('switches to Settings tab and shows Unpair', (tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: MaterialApp(home: HomeShell(store: GatewayStore(_MemKv()))),
    ));
    await tester.pump();

    expect(find.text('Sessions'), findsWidgets);
    await tester.tap(find.text('Settings'));
    await tester.pumpAndSettle();
    expect(find.text('Unpair'), findsOneWidget);
  });
}
