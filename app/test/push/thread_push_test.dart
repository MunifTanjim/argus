import 'package:argus/push/push_controller.dart';
import 'package:argus/push/push_message.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a push naming an agent thread opens the thread', () {
    final sessions = <String>[];
    final threads = <String>[];
    routePushOpen(
      const PushMessage(
        data: {'session_id': 'A:s1', 'pagent_thread_id': 'A:t1'},
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
        data: {'session_id': 'A:s1', 'pagent_thread_id': 'A:t1'},
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
}
