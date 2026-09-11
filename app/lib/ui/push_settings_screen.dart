import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/aggregate.dart' show pushGoneCode;
import '../push/push_controller.dart';
import '../push/pushport_config.dart';
import '../state/push.dart';
import '../transport/jsonrpc.dart';
import 'responsive.dart';
import 'theme.dart';

/// Push notifications settings: the active backend, a test button, a provider
/// picker, and (when UnifiedPush is active) a distributor picker.
class PushSettingsScreen extends ConsumerStatefulWidget {
  const PushSettingsScreen({super.key});

  @override
  ConsumerState<PushSettingsScreen> createState() => _PushSettingsScreenState();
}

class _PushSettingsScreenState extends ConsumerState<PushSettingsScreen> {
  List<String> _distributors = [];
  String? _currentDistributor;
  String? _activeProvider;
  bool _loading = true;
  bool? _registered;
  StreamSubscription<String>? _failureSub;
  StreamSubscription<bool>? _regSub;

  PushController get _controller => ref.read(pushControllerProvider);

  bool get _showDistributorPicker => _activeProvider == 'unifiedpush';

  @override
  void initState() {
    super.initState();
    _registered = _controller.lastRegistration;
    _activeProvider = _controller.activeBackend;
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
    await _controller.refreshServerInfo();
    if (!mounted) return;
    setState(() {
      _distributors = distributors;
      _currentDistributor = current ?? _currentDistributor;
      _activeProvider = _controller.activeBackend;
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
          : CenteredBody(
              child: ListView(
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
                  _header('Provider'),
                  ..._providerBody(),
                  if (_showDistributorPicker) ...[
                    const SizedBox(height: 16),
                    _header('Distributor'),
                    ..._distributorBody(),
                  ],
                ],
              ),
            ),
    );
  }

  Widget _registrationStatus() {
    const red = Color(0xFFfb4934);
    final (icon, color, label) = switch (_registered) {
      true => (
        Icons.check_circle_outline,
        AppColors.accent,
        'Registered with gateway',
      ),
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
    child: Text(
      title.toUpperCase(),
      style: const TextStyle(
        color: AppColors.accent,
        fontSize: 12,
        fontWeight: FontWeight.w700,
      ),
    ),
  );

  List<Widget> _providerBody() {
    final options = [
      ('unifiedpush', 'UnifiedPush', 'Default — uses distributor apps on the device'),
      if (PushPortConfig.isConfigured && _controller.pushPortAvailable)
        ('pushport/fcm', 'PushPort / FCM', 'Firebase Cloud Messaging with on-device decrypt'),
    ];
    return [
      RadioGroup<String>(
        groupValue: _activeProvider,
        onChanged: _selectProvider,
        child: Column(
          children: [
            for (final (value, label, subtitle) in options)
              RadioListTile<String>(
                contentPadding: EdgeInsets.zero,
                value: value,
                title: Text(label),
                subtitle: Text(
                  subtitle,
                  style: const TextStyle(color: AppColors.dim, fontSize: 11),
                ),
              ),
          ],
        ),
      ),
    ];
  }

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
      const Text(
        'Pick the distributor to deliver push:',
        style: TextStyle(color: AppColors.text, fontSize: 13),
      ),
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
                subtitle: Text(
                  d,
                  style: const TextStyle(color: AppColors.dim, fontSize: 11),
                ),
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
    } on RpcError catch (e) {
      if (e.code == pushGoneCode) {
        await _recoverGoneAndRetry();
        return;
      }
    } catch (_) {}
    await _reregisterAndRetry();
  }

  Future<void> _recoverGoneAndRetry() async {
    if (mounted) _toast('Push endpoint expired — refreshing…');
    final ok = await _controller.reregister(force: true);
    if (!mounted) return;
    if (!ok) {
      _toast(
        "Could not refresh the push endpoint. Clear the app's data or "
        'reinstall to mint a new push token, then try again.',
      );
      return;
    }
    await _sendTestThenToast(
      onSuccess: 'Refreshed and sent test — check your notifications',
      onFailure: 'Refreshed, but the test still failed — the push token may be '
          "dead. Clear the app's data or reinstall to mint a new one.",
    );
  }

  Future<void> _reregisterAndRetry() async {
    if (mounted) _toast('Re-registering with the gateway…');
    final ok = await _controller.reregister();
    if (!mounted) return;
    if (!ok) {
      _toast('Re-registration failed — check the connection and distributor');
      return;
    }
    await _sendTestThenToast(
      onSuccess: 'Re-registered and sent test — check your notifications',
      onFailure: 'Re-registered, but test still failed — try again in a moment',
    );
  }

  Future<void> _sendTestThenToast({
    required String onSuccess,
    required String onFailure,
  }) async {
    try {
      await _controller.sendTest();
      if (mounted) _toast(onSuccess);
    } catch (e) {
      if (mounted) _toast('$onFailure ($e)');
    }
  }

  Future<void> _selectProvider(String? name) async {
    if (name == null) return;
    final prev = _activeProvider;
    setState(() => _activeProvider = name);
    try {
      await _controller.useProvider(name);
      await _load();
      if (mounted) _toast('Using $name for push');
    } catch (e) {
      if (mounted) setState(() => _activeProvider = prev);
      if (mounted) _toast('Failed to switch to $name: $e');
    }
  }

  Future<void> _selectDistributor(String? d) async {
    if (d == null) return;
    setState(() => _currentDistributor = d);
    await _controller.useDistributor(d);
    await _load();
    if (mounted) _toast('Using ${_distributorLabel(d)} for push');
  }

  void _toast(String msg) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
}
