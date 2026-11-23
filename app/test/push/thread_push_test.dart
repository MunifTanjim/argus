import 'package:argus/push/notifications.dart' show threadTapPrefix;
import 'package:argus/push/push_controller.dart';
import 'package:argus/push/push_message.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a push naming a bot thread opens the thread', () {
    final sessions = <String>[];
    final threads = <String>[];
    routePushOpen(
      const PushMessage(
        data: {'session_id': 'A:s1', 'bot_thread_id': 'A:t1'},
      ),
      onSession: sessions.add,
      onThread: threads.add,
    );
    expect(threads, ['A:t1']);
    expect(sessions, isEmpty);
  });

  test('a push without a thread opens its session', () {
    final sessions = <String>[];
    final threads = <String>[];
    routePushOpen(
      const PushMessage(data: {'session_id': 'A:s1'}),
      onSession: sessions.add,
      onThread: threads.add,
    );
    expect(sessions, ['A:s1']);
    expect(threads, isEmpty);
  });

  test('a thread push falls back to its session when threads are not handled', () {
    final sessions = <String>[];
    routePushOpen(
      const PushMessage(
        data: {'session_id': 'A:s1', 'bot_thread_id': 'A:t1'},
      ),
      onSession: sessions.add,
    );
    expect(sessions, ['A:s1']);
  });

  test('a push with neither does nothing', () {
    routePushOpen(
      const PushMessage(),
      onSession: (_) => fail('no session'),
      onThread: (_) => fail('no thread'),
    );
  });

  test('a native tap marked as a thread opens the thread', () {
    // The iOS tap handler sends `bot_thread:<node>:<thread>` for a bot push.
    final sessions = <String>[];
    final threads = <String>[];
    routeTap('${threadTapPrefix}A:t1', onSession: sessions.add, onThread: threads.add);
    routeTap('A:s1', onSession: sessions.add, onThread: threads.add);
    expect(threads, ['A:t1']);
    expect(sessions, ['A:s1']);
  });
}
