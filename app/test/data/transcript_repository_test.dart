// app/test/data/transcript_repository_test.dart
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/transcript_repository.dart';
import 'package:argus/state/transcript.dart';

void main() {
  test('open returns null when there is no connection', () {
    final repo = TranscriptRepositoryRemote(() => null);
    final sub = repo.open(
      sessionId: 's',
      store: TranscriptNotifier(),
    );
    expect(sub, isNull);
  });
}
