import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/gateway.dart' show credentialsProvider;
import '../state/projects_api.dart' show actionError;
import '../state/server_api.dart';
import 'theme.dart';

/// Offers the latest release for a node, or for the gateway when [nodeId] is
/// null. Shows nothing while the target is current or cannot upgrade.
class UpgradeTile extends ConsumerStatefulWidget {
  const UpgradeTile({
    super.key,
    required this.nodeId,
    required this.label,
    required this.version,
  });

  final String? nodeId;
  final String label;
  final String version;

  @override
  ConsumerState<UpgradeTile> createState() => _UpgradeTileState();
}

class _UpgradeTileState extends ConsumerState<UpgradeTile> {
  UpgradeCheck? _check;
  String? _status;
  var _busy = false;
  var _gen = 0;
  Timer? _recheck;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _recheck?.cancel();
    super.dispose();
  }

  @override
  void didUpdateWidget(UpgradeTile old) {
    super.didUpdateWidget(old);
    if (old.version == widget.version && old.nodeId == widget.nodeId) return;
    _gen++;
    _recheck?.cancel();
    setState(() {
      _busy = false;
      _status = null;
      _check = null;
    });
    _load();
  }

  bool _stale(int gen) => !mounted || gen != _gen;

  // A failed check hides the tile: an unreachable target already shows as
  // offline elsewhere.
  Future<void> _load() async {
    final gen = _gen;
    try {
      final c = await ref.read(serverApiProvider).checkUpgrade(widget.nodeId);
      if (!_stale(gen)) setState(() => _check = c);
    } catch (_) {}
  }

  Future<void> _upgrade(UpgradeCheck c) async {
    final gen = _gen;
    final quickTunnel = widget.nodeId == null && isQuickTunnelUrl(ref.read(credentialsProvider)?.url);
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Upgrade ${widget.label}?'),
        content: Text('${c.current} → ${c.latest}. ${widget.label} restarts after the upgrade.'
            '${quickTunnel ? ' The quick tunnel gets a new URL on restart, so this app must pair again.' : ''}'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
          TextButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('Upgrade')),
        ],
      ),
    );
    if (ok != true || _stale(gen)) return;
    setState(() => _busy = true);
    try {
      final (from, to) = await ref.read(serverApiProvider).upgrade(widget.nodeId);
      if (_stale(gen)) return;
      setState(() => _status = to == from
          ? 'Already on the latest version.'
          : 'Upgraded to $to. Restarting…');
      // A restart into the same version (dev builds) never updates the
      // widget, so look again once the target is back.
      if (to != from) {
        _recheck = Timer(const Duration(seconds: 15), () {
          if (_stale(gen)) return;
          setState(() {
            _busy = false;
            _status = null;
          });
          _load();
        });
      }
    } on RestartFailed catch (e) {
      if (_stale(gen)) return;
      setState(() {
        _busy = false;
        _status = 'Upgraded to ${e.to}. Restart failed: ${actionError(e.error)}';
      });
    } catch (e) {
      if (!mounted || gen != _gen) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Upgrade failed: ${actionError(e)}')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = _check;
    if (c == null || !c.available) return const SizedBox.shrink();
    final status = _status;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      title: Text('Version ${c.latest} available'),
      subtitle: status == null
          ? null
          : Text(status, style: const TextStyle(color: AppColors.dim, fontSize: 11)),
      trailing: TextButton(
        onPressed: _busy ? null : () => _upgrade(c),
        child: const Text('Upgrade'),
      ),
    );
  }
}

/// A cloudflare quick tunnel gets a new random hostname each time the gateway
/// starts.
bool isQuickTunnelUrl(String? url) =>
    Uri.tryParse(url ?? '')?.host.endsWith('.trycloudflare.com') ?? false;
