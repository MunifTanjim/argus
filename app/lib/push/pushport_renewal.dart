import 'package:meta/meta.dart';

import 'push_provider.dart';
import 'pushport_client.dart';

typedef PushPortMint = ({PushPortSubscription sub, PushTarget target});

/// Nothing tells the app when a PushPort endpoint lapses — unlike a UnifiedPush
/// distributor, which hands over a replacement through onNewEndpoint — so the
/// app watches the clock itself and re-subscribes before the lease runs out.
mixin PushPortRenewal {
  void Function(PushTarget)? _onTarget;
  PushLease _lease = PushLease.none;
  Future<void>? _inFlight;

  /// A mint that was still in flight when the provider stopped must not write
  /// its endpoint into the provider that restarted.
  int _epoch = 0;

  /// Called under the overlap guard, so implementations never run concurrently.
  @protected
  Future<PushPortMint> mintSubscription();

  PushLease get lease => _lease;

  @protected
  Future<void> subscribe(void Function(PushTarget) onTarget) {
    _onTarget = onTarget;
    return _mint();
  }

  /// Does nothing before [subscribe] or after [forgetLease], when there is
  /// nobody to report a target to.
  @protected
  Future<void> renew() async {
    if (_onTarget == null) return;
    await _mint();
  }

  /// The relay has no unsubscribe call, so the old endpoint stays alive there
  /// until its lease runs out. The gateway is told to stop pushing by the
  /// controller, so nothing reaches it.
  @protected
  void forgetLease() {
    _onTarget = null;
    _lease = PushLease.none;
    _inFlight = null;
    _epoch++;
  }

  /// Two attaches can overlap, and each would otherwise mint an endpoint the
  /// other discards, leaving the gateway registered against the loser.
  Future<void> _mint() {
    final epoch = _epoch;
    return _inFlight ??= _mintOnce(epoch).whenComplete(() {
      if (epoch == _epoch) _inFlight = null;
    });
  }

  Future<void> _mintOnce(int epoch) async {
    final minted = await mintSubscription();
    if (epoch != _epoch) return;
    _lease = PushLease.granted(minted.sub.expiresAt);
    _onTarget?.call(minted.target);
  }
}
