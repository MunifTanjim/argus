import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/host_info.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/host_api.dart';
import 'package:argus/ui/node_screen.dart';

class _FakeApi implements HostApi {
  _FakeApi(this.next);
  HostInfo next;
  Completer<HostInfo>? gate;
  Object? failure;
  var infoCalls = 0;
  final set = <String>[];

  @override
  Future<HostInfo> info(String nodeId) async {
    infoCalls++;
    if (failure != null) throw failure!;
    if (gate != null) return gate!.future;
    return next;
  }

  @override
  Future<HostWakelock> setWakelock(String nodeId, String until) async {
    set.add(until);
    next = HostInfo(
      os: next.os,
      uptimeSeconds: next.uptimeSeconds,
      battery: next.battery,
      wakelock: HostWakelock(until: until),
    );
    return next.wakelock;
  }
}

Future<void> _pump(WidgetTester tester, _FakeApi api, {bool wakelock = true}) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [
      hostApiProvider.overrideWithValue(api),
      serverInfoProvider.overrideWith((ref) async => ServerInfo(
        version: '',
        nodes: [NodeRef('A', 'macbook', hostWakelockSupported: wakelock)],
      )),
    ],
    child: const MaterialApp(
      home: NodeScreen(nodeId: 'A', label: 'macbook', pollInterval: Duration(seconds: 1)),
    ),
  ));
  await tester.pump();
}

const _full = HostInfo(
  os: 'macOS 26.0.1 (arm64)',
  uptimeSeconds: 274320,
  battery: HostBattery(percent: 82, state: 'charging'),
  wakelock: HostWakelock(),
);

void main() {
  testWidgets('shows os, uptime, battery, and the switch', (tester) async {
    await _pump(tester, _FakeApi(_full));
    expect(find.text('macOS 26.0.1 (arm64)'), findsOneWidget);
    expect(find.text('3d 4h 12m'), findsOneWidget);
    expect(find.text('82% · charging'), findsOneWidget);
    expect(find.text('Keep awake'), findsOneWidget);
    expect(find.text('This node can sleep when idle.'), findsOneWidget);
  });

  testWidgets('hides battery and switch when not reported', (tester) async {
    await _pump(tester, _FakeApi(const HostInfo(uptimeSeconds: 60, wakelock: HostWakelock())), wakelock: false);
    expect(find.text('OS'), findsNothing);
    expect(find.text('Battery'), findsNothing);
    expect(find.text('Keep awake'), findsNothing);
  });

  testWidgets('switch on picks a duration; switch off clears', (tester) async {
    final api = _FakeApi(_full);
    await _pump(tester, api);
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Until I turn it off'));
    await tester.pumpAndSettle();
    expect(api.set, [wakelockIndefinite]);
    expect(find.text('Awake until you turn it off.'), findsOneWidget);
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    expect(api.set, [wakelockIndefinite, '']);
    expect(find.text('This node can sleep when idle.'), findsOneWidget);
  });

  testWidgets('the title shows the node label', (tester) async {
    await _pump(tester, _FakeApi(_full));
    expect(find.descendant(of: find.byType(AppBar), matching: find.text('macbook')), findsOneWidget);
  });

  testWidgets('subtitle shows the end time, with the date on another day', (tester) async {
    HostInfo awake(DateTime local) => HostInfo(
      uptimeSeconds: 60,
      wakelock: HostWakelock(until: local.toUtc().toIso8601String()),
    );
    final now = DateTime.now();
    await _pump(tester, _FakeApi(awake(DateTime(now.year, now.month, now.day, 23, 59))));
    expect(find.text('Awake until 23:59.'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    await _pump(tester, _FakeApi(awake(DateTime(2020, 1, 5, 14, 30))));
    expect(find.text('Awake until Jan 5 14:30.'), findsOneWidget);
  });

  testWidgets('the switch shows the returned wakelock before the refetch', (tester) async {
    final api = _FixedSetApi(_full);
    await _pump(tester, api);
    api.gate = Completer<HostInfo>();
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Until I turn it off'));
    await tester.pumpAndSettle();
    expect(find.text('Awake until you turn it off.'), findsOneWidget);
    expect(tester.widget<Switch>(find.byType(Switch)).value, isTrue);
  });

  testWidgets('the switch is disabled while setWakelock is pending', (tester) async {
    final api = _SlowSetApi(_full);
    await _pump(tester, api);
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Until I turn it off'));
    await tester.pumpAndSettle();
    expect(tester.widget<Switch>(find.byType(Switch)).onChanged, isNull);
    api.setGate.complete();
    await tester.pumpAndSettle();
    expect(tester.widget<Switch>(find.byType(Switch)).onChanged, isNotNull);
  });

  testWidgets('polls while open', (tester) async {
    final api = _FakeApi(_full);
    await _pump(tester, api);
    final first = api.infoCalls;
    await tester.pump(const Duration(seconds: 1));
    await tester.pump();
    expect(api.infoCalls, greaterThan(first));
  });

  testWidgets('shows the error with Retry', (tester) async {
    final api = _FakeApi(_full)..failure = StateError('not connected');
    await _pump(tester, api);
    expect(find.textContaining('not connected'), findsOneWidget);
    api.failure = null;
    await tester.tap(find.text('Retry'));
    await tester.pump();
    await tester.pump();
    expect(find.text('3d 4h 12m'), findsOneWidget);
  });

  testWidgets('leaving during a fetch does not throw', (tester) async {
    final api = _FakeApi(_full)..gate = Completer<HostInfo>();
    await _pump(tester, api);
    await tester.pumpWidget(const SizedBox());
    api.gate!.complete(_full);
    await tester.pump(const Duration(seconds: 2));
    expect(tester.takeException(), isNull);
  });

  testWidgets('leaving while the duration sheet is open does not throw', (tester) async {
    await _pump(tester, _FakeApi(_full));
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox());
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('leaving while setWakelock is pending does not throw', (tester) async {
    final api = _SlowSetApi(_full);
    await _pump(tester, api);
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Until I turn it off'));
    await tester.pump();
    await tester.pumpWidget(const SizedBox());
    api.setGate.complete();
    await tester.pump(const Duration(seconds: 2));
    expect(tester.takeException(), isNull);
    expect(api.infoCalls, 1);
  });

  testWidgets('retry during a pending fetch does not stack requests', (tester) async {
    final api = _FakeApi(_full)..failure = StateError('boom');
    await _pump(tester, api);
    api.failure = null;
    api.gate = Completer<HostInfo>();
    await tester.tap(find.text('Retry'));
    await tester.pump();
    expect(api.infoCalls, 2);
    await tester.tap(find.text('Retry'), warnIfMissed: false);
    await tester.pump();
    expect(api.infoCalls, 2);
  });

  testWidgets('a toggle during a pending fetch refetches after the reply', (tester) async {
    final api = _FakeApi(_full);
    await _pump(tester, api);
    final gate = api.gate = Completer<HostInfo>();
    await tester.pump(const Duration(seconds: 1));
    expect(api.infoCalls, 2);
    await tester.tap(find.byType(Switch));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Until I turn it off'));
    await tester.pump();
    expect(api.infoCalls, 2);
    api.gate = null;
    gate.complete(_full);
    await tester.pump();
    await tester.pump();
    expect(api.infoCalls, 3);
  });
}

class _SlowSetApi extends _FakeApi {
  _SlowSetApi(super.next);
  final setGate = Completer<void>();

  @override
  Future<HostWakelock> setWakelock(String nodeId, String until) async {
    await setGate.future;
    return super.setWakelock(nodeId, until);
  }
}

// setWakelock returns the new state without changing what info returns.
class _FixedSetApi extends _FakeApi {
  _FixedSetApi(super.next);

  @override
  Future<HostWakelock> setWakelock(String nodeId, String until) async {
    set.add(until);
    return HostWakelock(until: until);
  }
}
