import 'dart:typed_data';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/e2e.dart';
import 'gateway.dart';

class TrustSummary {
  const TrustSummary({
    required this.connected,
    required this.isLocked,
    required this.isAuthorized,
    required this.isDisabled,
    required this.head,
    this.signers = const [],
  });
  const TrustSummary.disconnected()
      : connected = false,
        isLocked = null,
        isAuthorized = false,
        isDisabled = false,
        head = null,
        signers = const [];

  final bool connected;
  final bool? isLocked; // null = open network / unknown
  final bool isAuthorized;
  final bool isDisabled;
  final Uint8List? head;
  final List<Uint8List> signers;
}

/// The persisted device identity (Curve25519). Works offline.
final deviceIdentityProvider = FutureProvider<KeyPair>(
    (ref) async => ref.read(clientIdentityStoreProvider).loadOrCreate());

/// The live trust status from the active E2E client (disconnected when none).
final trustSummaryProvider = Provider<TrustSummary>((ref) {
  ref.watch(connStateProvider); // recompute on connection-state changes
  final client = ref.watch(gatewayProvider)?.client;
  if (client is! E2EClient) return const TrustSummary.disconnected();
  return TrustSummary(
    connected: true,
    isLocked: client.isLocked,
    isAuthorized: client.isAuthorized,
    isDisabled: client.isDisabled,
    head: client.trustHead,
    signers: client.trustSigners ?? const [],
  );
});
