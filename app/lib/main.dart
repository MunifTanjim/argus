import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'pairing/gateway_store.dart';
import 'pairing/legacy_migration.dart';
import 'push/unifiedpush_background.dart';
import 'state/gateway.dart';
import 'state/profiles.dart';
import 'state/push.dart';
import 'transport/connection.dart';
import 'ui/home_shell.dart';
import 'ui/profiles_screen.dart';
import 'ui/route_observer.dart';
import 'ui/theme.dart';

Future<void> main(List<String> args) async {
  WidgetsFlutterBinding.ensureInitialized();
  // Register UnifiedPush callbacks here so they also run in the headless
  // background isolate (started with --unifiedpush-bg) when the app is killed,
  // letting an incoming push raise a notification.
  await initUnifiedPush();
  // Headless background launch: handle the push, don't build the UI.
  if (args.contains('--unifiedpush-bg')) return;
  runApp(const ProviderScope(child: ArgusApp()));
}

class ArgusApp extends ConsumerStatefulWidget {
  const ArgusApp({super.key});

  @override
  ConsumerState<ArgusApp> createState() => _ArgusAppState();
}

class _ArgusAppState extends ConsumerState<ArgusApp>
    with WidgetsBindingObserver {
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    migrateLegacyOnce(const FlutterSecureKv()).then((_) async {
      final creds = await restoreActiveCredentials(
        ref.read(profileStoreProvider),
        ref.read(keyLibraryStoreProvider),
        ref.read(sshKeyStoreProvider),
      );
      if (creds != null) {
        ref.read(credentialsProvider.notifier).state = creds;
      }
    }).whenComplete(() {
      if (mounted) setState(() => _loaded = true);
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Timers are frozen while backgrounded, so a pending backoff retry won't
    // fire on its own. On resume, kick a stuck connection back to life now
    // instead of waiting it out (a healthy link is left alone — keepalive
    // verifies it).
    if (state != AppLifecycleState.resumed) return;
    final mgr = ref.read(gatewayProvider);
    if (mgr != null && mgr.state != ConnState.connected) mgr.reconnectNow();
  }

  @override
  Widget build(BuildContext context) {
    final creds = ref.watch(credentialsProvider);
    // Materialize the gateway connection whenever credentials exist.
    ref.watch(gatewayProvider);
    // Materialize push: starts a backend, requests permission, wires tap routing.
    ref.watch(pushControllerProvider);

    return MaterialApp(
      title: 'argus',
      theme: buildArgusTheme(),
      navigatorObservers: [appRouteObserver],
      home: !_loaded
          ? const Scaffold(body: Center(child: CircularProgressIndicator()))
          : creds == null
              ? const ProfilesScreen()
              : const HomeShell(),
    );
  }
}
