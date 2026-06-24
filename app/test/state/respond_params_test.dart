import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/respond_params.dart';

QuestionSpec _q({
  String question = 'Pick one',
  bool multi = false,
  List<String> options = const ['A', 'B'],
}) =>
    QuestionSpec.fromJson(jsonDecode(jsonEncode({
      'question': question,
      'multi_select': multi,
      'options': options,
    })) as Map<String, dynamic>);

void main() {
  group('questionRespond', () {
    test('single-select chosen option', () {
      final d = QuestionDraft()..chosen = 1;
      expect(
        questionRespond(sessionId: 's', questions: [_q()], drafts: [d]),
        {
          'session_id': 's',
          'kind': 'question',
          'behavior': 'allow',
          'answers': {'Pick one': 'B'},
        },
      );
    });
    test('single-select custom text', () {
      final q = _q();
      final d = QuestionDraft()
        ..chosen = otherIndex(q)
        ..custom = 'mine';
      expect(
        questionRespond(sessionId: 's', questions: [q], drafts: [d])!['answers'],
        {'Pick one': 'mine'},
      );
    });
    test('multi-select collects labels plus custom', () {
      final q = _q(multi: true);
      final d = QuestionDraft()
        ..toggles.addAll({0, otherIndex(q)})
        ..custom = 'extra';
      expect(
        questionRespond(sessionId: 's', questions: [q], drafts: [d])!['answers'],
        {'Pick one': ['A', 'extra']},
      );
    });
    test('unanswered question omitted; all-unanswered returns null', () {
      expect(
        questionRespond(
            sessionId: 's', questions: [_q()], drafts: [QuestionDraft()]),
        isNull,
      );
    });
  });

  group('clarifyRespond', () {
    test('includes partial answers', () {
      final d = QuestionDraft()..chosen = 0;
      expect(
        clarifyRespond(sessionId: 's', questions: [_q()], drafts: [d]),
        {
          'session_id': 's',
          'kind': 'question',
          'question_action': 'chat',
          'answers': {'Pick one': 'A'},
        },
      );
    });
    test('valid with no answers (omits answers key)', () {
      expect(
        clarifyRespond(
            sessionId: 's', questions: [_q()], drafts: [QuestionDraft()]),
        {'session_id': 's', 'kind': 'question', 'question_action': 'chat'},
      );
    });
  });
}
