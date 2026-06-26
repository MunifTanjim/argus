import 'package:argus/push/notifications.dart';
import 'package:argus/push/push_message.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  PushMessage msg(String? sessionId) => PushMessage(
        title: 't',
        body: 'b',
        data: {'session_id': ?sessionId},
      );

  group('sessionNotificationId', () {
    // A deterministic FNV-1a hash, NOT String.hashCode, because hashCode is not
    // stable across isolates/runs: the headless background isolate would compute
    // a different id for the same session than the foreground one, so duplicate
    // notifications would stack instead of replacing and cancelForSession could
    // not target a notification raised by the other isolate. Golden values are
    // cross-checked against a reference FNV-1a (& 0x7fffffff) implementation.
    test('is a stable FNV-1a id (golden values), not String.hashCode', () {
      expect(sessionNotificationId('home:default:%3'), 594889565);
      expect(sessionNotificationId('node1:abc'), 485007102);
      expect(sessionNotificationId('host:%1'), 1356194493);
    });

    test('is deterministic for the same session id', () {
      expect(
        sessionNotificationId('home:default:%3'),
        sessionNotificationId('home:default:%3'),
      );
    });

    test('is always a positive 31-bit int', () {
      for (final id in ['home:default:%3', 'node1:abc', '', 'x']) {
        final n = sessionNotificationId(id);
        expect(n, greaterThanOrEqualTo(0));
        expect(n, lessThanOrEqualTo(0x7fffffff));
      }
    });

    test('differs for different session ids', () {
      expect(
        sessionNotificationId('home:default:%3'),
        isNot(sessionNotificationId('home:default:%4')),
      );
    });
  });

  group('graceful degradation', () {
    // The notification platform plugin is unavailable in the test environment.
    // cancelForSession (called from SessionDetailScreen.initState) must degrade
    // to a no-op instead of throwing and crashing the screen.
    test('cancelForSession does not throw when the plugin is unavailable',
        () async {
      TestWidgetsFlutterBinding.ensureInitialized();
      await PushNotifications.instance.cancelForSession('home:default:%3');
    });
  });

  group('suppressForActive', () {
    test('suppresses a notification for the actively viewed session', () {
      expect(suppressForActive('host:%1', msg('host:%1')), isTrue);
    });

    test('does not suppress a different session', () {
      expect(suppressForActive('host:%1', msg('host:%2')), isFalse);
    });

    test('does not suppress when no session is active', () {
      expect(suppressForActive(null, msg('host:%1')), isFalse);
    });

    test('does not suppress a message that carries no session id', () {
      expect(suppressForActive('host:%1', msg(null)), isFalse);
    });
  });
}
