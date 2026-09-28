import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/ui/sign_out.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Root extends ConsumerWidget {
  const _Root(this.navigatorKey);
  final GlobalKey<NavigatorState> navigatorKey;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    popToRootOnSignOut(ref, navigatorKey);
    final creds = ref.watch(credentialsProvider);
    return MaterialApp(
      navigatorKey: navigatorKey,
      home: Text(creds == null ? 'connect' : 'home'),
    );
  }
}

void main() {
  testWidgets('clearing credentials pops routes above the root', (
    tester,
  ) async {
    final key = GlobalKey<NavigatorState>();
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(c.dispose);
    c.read(credentialsProvider.notifier).state = const GatewayCredentials(
      'ws://x',
      't',
    );
    await tester.pumpWidget(
      UncontrolledProviderScope(container: c, child: _Root(key)),
    );
    key.currentState!.push(
      MaterialPageRoute<void>(builder: (_) => const Text('settings')),
    );
    await tester.pumpAndSettle();
    expect(find.text('settings'), findsOneWidget);

    c.read(credentialsProvider.notifier).state = null;
    await tester.pumpAndSettle();
    expect(find.text('settings'), findsNothing);
    expect(find.text('connect'), findsOneWidget);
  });
}
