import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../push/push_controller.dart';
import '../state/push.dart';
import 'theme.dart';

/// Push notifications settings: the active backend, a test button, and a
/// UnifiedPush distributor picker (the embedded FCM distributor appears here as
/// "argus" alongside any installed external distributors).
class PushSettingsScreen extends ConsumerStatefulWidget {
  const PushSettingsScreen({super.key});

  @override
  ConsumerState<PushSettingsScreen> createState() => _PushSettingsScreenState();
}

class _PushSettingsScreenState extends ConsumerState<PushSettingsScreen> {
  List<String> _distributors = [];
  String? _currentDistributor;
  bool _loading = true;
  bool? _registered;
  StreamSubscription<String>? _failureSub;
  StreamSubscription<bool>? _regSub;

  PushController get _controller => ref.read(pushControllerProvider);

  @override
  void initState() {
    super.initState();
    // Seed from the last known result: registration usually completes on connect,
    // before this screen (and its broadcast subscription) exists.
    _registered = _controller.lastRegistration;
    _failureSub = _controller.pushFailures.listen((reason) {
      if (mounted) _toast('UnifiedPush registration failed: $reason');
    });
    _regSub = _controller.registrations.listen((ok) {
      if (!mounted) return;
      setState(() => _registered = ok);
      if (!ok) _toast('Failed to register with the gateway — will retry');
    });
    _load();
  }

  @override
  void dispose() {
    _failureSub?.cancel();
    _regSub?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    final distributors = await _controller.distributors();
    final current = await _controller.currentDistributor();
    if (!mounted) return;
    setState(() {
      _distributors = distributors;
      // Keep the existing selection if the plugin hasn't acknowledged one yet
      // (ack is async, especially for the embedded FCM distributor).
      _currentDistributor = current ?? _currentDistributor;
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final active = _controller.activeBackend;
    return Scaffold(
      appBar: AppBar(title: const Text('Push notifications')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView(
              padding: const EdgeInsets.all(16),
              children: [
                _registrationStatus(),
                const SizedBox(height: 8),
                OutlinedButton.icon(
                  onPressed: active == null ? null : _sendTest,
                  icon: const Icon(Icons.notifications_active_outlined),
                  label: const Text('Send test notification'),
                ),
                const SizedBox(height: 16),
                _header('Distributor'),
                ..._distributorBody(),
              ],
            ),
    );
  }

  Widget _registrationStatus() {
    const red = Color(0xFFfb4934); // gruvbox red — matches status usage elsewhere
    final (icon, color, label) = switch (_registered) {
      true => (Icons.check_circle_outline, AppColors.accent, 'Registered with gateway'),
      false => (Icons.error_outline, red, 'Not registered — retrying'),
      null => (Icons.hourglass_empty, AppColors.dim, 'Registration pending'),
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 14, color: color),
        const SizedBox(width: 4),
        Text(label, style: TextStyle(color: color, fontSize: 12)),
      ],
    );
  }

  Widget _header(String title) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(title.toUpperCase(),
            style: const TextStyle(
                color: AppColors.accent,
                fontSize: 12,
                fontWeight: FontWeight.w700)),
      );

  List<Widget> _distributorBody() {
    if (_distributors.isEmpty) {
      return const [
        Text(
          'No distributor detected. On devices with Google Play services the '
          'app\'s built-in distributor should appear automatically; otherwise '
          'install a UnifiedPush distributor (e.g. ntfy).',
          style: TextStyle(color: AppColors.text, fontSize: 13),
        ),
      ];
    }
    return [
      const Text('Pick the distributor to deliver push:',
          style: TextStyle(color: AppColors.text, fontSize: 13)),
      const SizedBox(height: 4),
      RadioGroup<String>(
        groupValue: _currentDistributor,
        onChanged: _selectDistributor,
        child: Column(
          children: [
            for (final d in _distributors)
              RadioListTile<String>(
                contentPadding: EdgeInsets.zero,
                value: d,
                title: Text(_distributorLabel(d)),
                subtitle: Text(d,
                    style:
                        const TextStyle(color: AppColors.dim, fontSize: 11)),
              ),
          ],
        ),
      ),
    ];
  }

  String _distributorLabel(String pkg) =>
      pkg == appPackageName ? 'argus (built-in)' : pkg.split('.').last;

  Future<void> _sendTest() async {
    try {
      await _controller.sendTest();
      if (mounted) _toast('Test notification sent — check your notifications');
      return;
    } catch (_) {
      // The gateway may have no/stale registration for this device. Re-register
      // and retry once.
    }
    if (mounted) _toast('Re-registering with the gateway…');
    final ok = await _controller.reregister();
    if (!mounted) return;
    if (!ok) {
      _toast('Re-registration failed — check the connection and distributor');
      return;
    }
    try {
      await _controller.sendTest();
      if (mounted) _toast('Re-registered and sent test — check your notifications');
    } catch (e) {
      if (mounted) {
        _toast('Re-registered, but test still failed — try again in a moment: $e');
      }
    }
  }

  Future<void> _selectDistributor(String? d) async {
    if (d == null) return;
    setState(() => _currentDistributor = d); // reflect the choice immediately
    await _controller.useDistributor(d);
    await _load();
    if (mounted) _toast('Using ${_distributorLabel(d)} for push');
  }

  void _toast(String msg) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
}
