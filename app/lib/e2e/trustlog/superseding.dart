import 'dart:typed_data';

import '../bytes.dart' show bytesEqual;
import 'assemble.dart' show chainEntries;
import 'codec.dart' show hashEntry, unmarshalEntry;
import 'entry.dart' show Kind;

/// Mirrors Go trustlog.SupersedingGenesis. Among [chains] not rooted at [own],
/// returns the genesis hash of the longest chain that carries no disable entry.
/// When every other offered root is disabled, returns the first such genesis so
/// a device stuck on a superseded root still learns it is alone. Returns null
/// when no chain decodes.
Uint8List? supersedingGenesis(List<Uint8List> chains, Uint8List? own) {
  Uint8List? fallback;
  Uint8List? best;
  var bestLen = 0;
  for (final chain in chains) {
    final List<Uint8List> raw;
    try {
      raw = chainEntries(chain);
    } catch (_) {
      continue;
    }
    if (raw.isEmpty) continue;
    final Uint8List genesis;
    try {
      genesis = hashEntry(unmarshalEntry(raw.first));
    } catch (_) {
      continue;
    }
    if (own != null && bytesEqual(genesis, own)) continue;
    fallback ??= genesis;
    if (_containsDisable(raw)) continue;
    if (raw.length > bestLen) {
      bestLen = raw.length;
      best = genesis;
    }
  }
  return best ?? fallback;
}

/// The result of [detectSupersession]: the app's pinned root is dead and the
/// network offers another. [genesis] is the successor root to name for the user.
/// [liveChain] is the exact chain to adopt on re-pin, or null when every offered
/// successor is itself disabled (the CLI "no new root yet" case).
class Supersession {
  const Supersession(this.genesis, this.liveChain);
  final Uint8List genesis;
  final Uint8List? liveChain;
}

/// Returns the successor root to name and, when it is live, the chain to adopt;
/// null when no other root is offered. Run only when the own pinned chain is
/// disabled, mirroring the node's detectSupersedingChain.
Supersession? detectSupersession(List<Uint8List> chains, Uint8List own) {
  final genesis = supersedingGenesis(chains, own);
  if (genesis == null) return null;
  Uint8List? liveChain;
  var liveLen = 0;
  for (final chain in chains) {
    final List<Uint8List> raw;
    try {
      raw = chainEntries(chain);
    } catch (_) {
      continue;
    }
    if (raw.isEmpty) continue;
    final Uint8List g;
    try {
      g = hashEntry(unmarshalEntry(raw.first));
    } catch (_) {
      continue;
    }
    if (!bytesEqual(g, genesis) || _containsDisable(raw)) continue;
    if (raw.length > liveLen) {
      liveLen = raw.length;
      liveChain = chain;
    }
  }
  return Supersession(genesis, liveChain);
}

bool _containsDisable(List<Uint8List> rawEntries) {
  for (final raw in rawEntries) {
    try {
      if (unmarshalEntry(raw).kind == Kind.disable) return true;
    } catch (_) {
      // One undecodable entry must not mask a disable elsewhere in the chain.
    }
  }
  return false;
}
