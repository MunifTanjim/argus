// app/test/core/command_test.dart
import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/command.dart';
import 'package:argus/core/result.dart';

void main() {
  test('Command0 transitions running -> completed and captures the value',
      () async {
    final cmd = Command0<int>(() async => const Result.ok(7));
    final states = <bool>[];
    cmd.addListener(() => states.add(cmd.running));

    expect(cmd.running, isFalse);
    await cmd.execute();

    expect(states, [true, false]); // running then idle
    expect(cmd.completed, isTrue);
    expect(cmd.error, isFalse);
    expect((cmd.result as Ok<int>).value, 7);
  });

  test('Command0 captures an error result', () async {
    final cmd = Command0<int>(() async => Result.error(Exception('x')));
    await cmd.execute();
    expect(cmd.error, isTrue);
    expect(cmd.completed, isFalse);
  });

  test('execute is ignored while already running', () async {
    var calls = 0;
    final gate = Completer<void>();
    final cmd = Command0<int>(() async {
      calls++;
      await gate.future;
      return const Result.ok(1);
    });

    final first = cmd.execute();
    await Future<void>.delayed(Duration.zero);
    // Second call while running is a no-op.
    await cmd.execute();
    expect(calls, 1);

    gate.complete();
    await first;
    expect(calls, 1);
  });

  test('Command1 passes its argument through', () async {
    final cmd = Command1<String, int>((a) async => Result.ok('v$a'));
    await cmd.execute(3);
    expect((cmd.result as Ok<String>).value, 'v3');
  });

  test('clearResult resets the captured result', () async {
    final cmd = Command0<int>(() async => const Result.ok(1));
    await cmd.execute();
    expect(cmd.result, isNotNull);
    cmd.clearResult();
    expect(cmd.result, isNull);
  });
}
