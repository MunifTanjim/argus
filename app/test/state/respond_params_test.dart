import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/respond_params.dart';

QuestionSpec _q({
  String question = 'Pick one',
  bool multi = false,
  bool allowNotes = false,
  List<String> options = const ['A', 'B'],
}) =>
    QuestionSpec.fromJson(jsonDecode(jsonEncode({
      'question': question,
      'multi_select': multi,
      'allow_notes': allowNotes,
      'options': options,
    })) as Map<String, dynamic>);

void main() {
  group('questionRespond', () {
    test('notes ride along for allow_notes questions, trimmed', () {
      final q = _q(allowNotes: true);
      final d = QuestionDraft()
        ..chosen = 1
        ..note = '  extra large ';
      expect(
        questionRespond(sessionId: 's', questions: [q], drafts: [d]),
        {
          'session_id': 's',
          'kind': 'question',
          'behavior': 'allow',
          'answers': {'Pick one': 'B'},
          'notes': {'Pick one': 'extra large'},
        },
      );
    });
    test('blank notes and non-notes questions send no notes', () {
      final d1 = QuestionDraft()
        ..chosen = 0
        ..note = '   ';
      final d2 = QuestionDraft()
        ..chosen = 0
        ..note = 'ignored';
      final p = questionRespond(
          sessionId: 's',
          questions: [_q(allowNotes: true), _q(question: 'Other')],
          drafts: [d1, d2])!;
      expect(p.containsKey('notes'), isFalse);
    });
    test('allow_notes question has no type-your-own entry', () {
      final q = _q(allowNotes: true);
      expect(otherIndex(q), -1);
    });
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

  group('request_id', () {
    test('echoed when the interaction has one', () {
      expect(
        optionRespond(
            sessionId: 's', kind: 'permission', value: 'allow', requestId: '7'),
        {
          'session_id': 's',
          'kind': 'permission',
          'request_id': '7',
          'option_value': 'allow',
        },
      );
      expect(interruptRespond(sessionId: 's', requestId: '7')['request_id'],
          '7');
    });
    test('omitted when empty', () {
      expect(
        optionRespond(
            sessionId: 's', kind: 'permission', value: 'allow', requestId: ''),
        isNot(contains('request_id')),
      );
    });
  });
}
