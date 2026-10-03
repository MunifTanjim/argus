import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/host_info.dart';
import '../state/control.dart';
import '../state/host_api.dart';
import '../state/projects_api.dart' show actionError;
import 'duration_sheet.dart';
import 'host_format.dart';
import 'responsive.dart';
import 'theme.dart';

class NodeScreen extends ConsumerStatefulWidget {
  const NodeScreen({
    super.key,
    required this.nodeId,
    required this.label,
    this.pollInterval = const Duration(seconds: 30),
  });

  final String nodeId;
  final String label;
  final Duration pollInterval;

  @override
  ConsumerState<NodeScreen> createState() => _NodeScreenState();
}

class _NodeScreenState extends ConsumerState<NodeScreen> {
  HostInfo? _info;
  Object? _error;
  Timer? _timer;
  var _inFlight = false;
  var _again = false;
  var _setting = false;

  @override
  void initState() {
    super.initState();
    _fetch();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  // The next poll waits for the last reply, so a slow link never stacks fetches.
  Future<void> _fetch() async {
    if (_inFlight) {
      _again = true;
      return;
    }
    _timer?.cancel();
    _inFlight = true;
    try {
      final info = await ref.read(hostApiProvider).info(widget.nodeId);
      if (!mounted) return;
      setState(() {
        _info = info;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e);
    } finally {
      _inFlight = false;
    }
    if (_again) {
      _again = false;
      _fetch();
    } else {
      _timer = Timer(widget.pollInterval, _fetch);
    }
  }

  Future<void> _onToggle(bool on) async {
    var until = '';
    if (on) {
      final picked = await pickUntil(
        context,
        indefiniteLabel: 'Until I turn it off',
        indefinite: wakelockIndefinite,
      );
      if (picked == null || !mounted) return;
      until = picked;
    }
    setState(() => _setting = true);
    try {
      final w = await ref.read(hostApiProvider).setWakelock(widget.nodeId, until);
      if (!mounted) return;
      final info = _info;
      if (info != null) {
        _info = HostInfo(
          os: info.os,
          uptimeSeconds: info.uptimeSeconds,
          battery: info.battery,
          wakelock: w,
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('wakelock failed: ${actionError(e)}')),
        );
      }
    } finally {
      if (mounted) setState(() => _setting = false);
    }
    if (!mounted) return;
    await _fetch();
  }

  String _awakeLabel(HostWakelock w) {
    if (!w.on) return 'This node can sleep when idle.';
    if (w.until == wakelockIndefinite) return 'Awake until you turn it off.';
    final dt = DateTime.tryParse(w.until)?.toLocal();
    if (dt == null) return 'Awake.';
    return 'Awake until ${untilClock(dt, DateTime.now())}.';
  }

  Widget _row(String label, String value) => ListTile(
    contentPadding: EdgeInsets.zero,
    title: Text(label),
    trailing: Text(value, style: const TextStyle(color: AppColors.secondary)),
  );

  @override
  Widget build(BuildContext context) {
    final info = _info;
    final error = _error;
    final wakelock = ref
            .watch(serverInfoProvider)
            .value
            ?.nodes
            .any((n) => n.id == widget.nodeId && n.hostWakelockSupported) ??
        false;
    return Scaffold(
      appBar: AppBar(title: Text(widget.label)),
      body: CenteredBody(
        child: error != null
            ? _errorBody(error)
            : info == null
            ? const Center(child: CircularProgressIndicator())
            : ListView(
                padding: const EdgeInsets.all(16),
                children: [
                  if (info.os.isNotEmpty) _row('OS', info.os),
                  _row('Uptime', formatUptime(info.uptimeSeconds)),
                  if (info.battery != null) _row('Battery', formatBattery(info.battery!)),
                  if (wakelock)
                    SwitchListTile(
                      contentPadding: EdgeInsets.zero,
                      value: info.wakelock.on,
                      title: const Text('Keep awake'),
                      subtitle: Text(
                        _awakeLabel(info.wakelock),
                        style: const TextStyle(color: AppColors.dim, fontSize: 11),
                      ),
                      onChanged: _setting ? null : _onToggle,
                    ),
                ],
              ),
      ),
    );
  }

  Widget _errorBody(Object error) => Padding(
    padding: const EdgeInsets.all(16),
    child: Row(
      children: [
        Expanded(
          child: Text(
            'Could not load host info: ${actionError(error)}',
            style: const TextStyle(color: AppColors.error, fontSize: 12),
          ),
        ),
        TextButton(onPressed: _fetch, child: const Text('Retry')),
      ],
    ),
  );
}
