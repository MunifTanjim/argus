import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:argus/state/device_identity.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/connection.dart';

void main() {
  test('startEquivPoll does not modify providers during a provider build', () async {
    final manager = ConnectionManager(
      connect: () async => throw UnimplementedError(),
      clientFactory: (_, __) async => throw UnimplementedError(),
    );
    // Mirrors gatewayProvider: the poll is started from inside a provider's build.
    final harness = Provider<Timer>((ref) => startEquivPoll(
          manager,
          ref.read(equivocationProvider.notifier),
          ref.read(trustSignatureProvider.notifier),
          interval: const Duration(days: 1),
        ));
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final timer = container.read(harness); // must not throw during build
    addTearDown(timer.cancel);

    // The immediate poll runs after the build, not during it.
    await Future<void>.delayed(Duration.zero);
    expect(
      container.read(trustSignatureProvider),
      equals(trustSignatureOf(const TrustSummary.disconnected())),
    );
  });
}
