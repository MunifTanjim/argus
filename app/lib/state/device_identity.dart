import 'dart:typed_data';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../e2e/e2e.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

class TrustSummary {
  const TrustSummary({
    required this.connected,
    required this.isLocked,
    required this.isAuthorized,
    required this.isDisabled,
    required this.tip,
    this.signers = const [],
    this.equivocation = false,
    this.supersededByGenesis,
    this.canAdoptSupersedingRoot = false,
  });
  const TrustSummary.disconnected()
    : connected = false,
      isLocked = null,
      isAuthorized = false,
      isDisabled = false,
      tip = null,
      signers = const [],
      equivocation = false,
      supersededByGenesis = null,
      canAdoptSupersedingRoot = false;

  final bool connected;
  final bool? isLocked; // null = open network / unknown
  final bool isAuthorized;
  final bool isDisabled;
  final Uint8List? tip;
  final List<Uint8List> signers;

  /// True when the E2E client has detected a trust-log equivocation: a tip read
  /// over one or more nodes' authenticated channels could not be reconciled with
  /// the resolved chain after the miss threshold. Warn-only; the session continues.
  final bool equivocation;

  final Uint8List? supersededByGenesis;
  final bool canAdoptSupersedingRoot;
}

/// Mirrors the CLI's lockHeadline: a superseded root is distinct from a
/// permanently disabled network.
enum TrustStatus {
  notConnected,
  openNetwork,
  supersededLiveRoot,
  supersededNoRoot,
  disabled, // break-glass, permanent; no successor offered
  authorized,
  awaitingAuthorization,
}

/// Supersession is checked before [TrustSummary.isDisabled] so a device stuck on
/// a dead, superseded root is told to re-pin rather than shown a dead-end
/// "disabled".
TrustStatus trustStatusOf(TrustSummary s) {
  if (!s.connected) return TrustStatus.notConnected;
  if (s.isLocked == null) return TrustStatus.openNetwork;
  if (s.supersededByGenesis != null) {
    return s.canAdoptSupersedingRoot
        ? TrustStatus.supersededLiveRoot
        : TrustStatus.supersededNoRoot;
  }
  if (s.isDisabled) return TrustStatus.disabled;
  if (s.isAuthorized) return TrustStatus.authorized;
  return TrustStatus.awaitingAuthorization;
}

/// Whether the signer set is worth verifying: only for a live root. A disabled or
/// superseded root is dead, so its signers no longer match the network and must
/// not be offered for comparison.
bool trustSignersVerifiable(TrustStatus s) =>
    s == TrustStatus.authorized || s == TrustStatus.awaitingAuthorization;

/// The persisted device identity (Curve25519). Works offline.
final deviceIdentityProvider = FutureProvider<KeyPair>(
  (ref) async => ref.read(clientIdentityStoreProvider).loadOrCreate(),
);

/// The trust status of [client], or disconnected when it is not an E2E client.
TrustSummary trustSummaryOf(GatewayClient? client) {
  // Widen to GatewayClient? so the is-E2EClient narrowing works regardless of
  // whether connection.dart exposes RpcClient? or GatewayClient? at the call site.
  if (client is! E2EClient) return const TrustSummary.disconnected();
  return TrustSummary(
    connected: true,
    isLocked: client.isLocked,
    isAuthorized: client.isAuthorized,
    isDisabled: client.isDisabled,
    tip: client.trustTip,
    signers: client.trustSigners ?? const [],
    equivocation: client.equivocation,
    supersededByGenesis: client.supersededByGenesis,
    canAdoptSupersedingRoot: client.canAdoptSupersedingRoot,
  );
}

/// A token that changes whenever any field of [s] changes. The resync poll writes
/// it (via [trustSignatureProvider]) so the trust page re-renders on a background
/// trust change, not only on reconnect.
String trustSignatureOf(TrustSummary s) {
  String hx(Uint8List? b) => b == null ? '' : hexEncode(b);
  return [
    s.connected,
    s.isLocked,
    s.isAuthorized,
    s.isDisabled,
    hx(s.tip),
    hx(s.supersededByGenesis),
    s.canAdoptSupersedingRoot,
    s.equivocation,
    s.signers.map(hx).join(','),
  ].join('|');
}

/// Bumped by the resync poll when the trust snapshot changes; watched by
/// [trustSummaryProvider] so a mid-session trust change re-renders the page.
final trustSignatureProvider = StateProvider<String>((ref) => '');

/// The live trust status from the active E2E client (disconnected when none).
final trustSummaryProvider = Provider<TrustSummary>((ref) {
  ref.watch(connStateProvider);
  ref.watch(trustSignatureProvider);
  return trustSummaryOf(ref.watch(gatewayProvider)?.client);
});
