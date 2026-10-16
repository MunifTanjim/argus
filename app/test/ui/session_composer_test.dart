import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/interaction_panel.dart';
import 'package:argus/ui/session_composer.dart';
import 'package:argus/ui/status_style.dart';
import 'package:argus/ui/theme.dart';
import '../support/fake_session_repository.dart';

class _Recording extends FakeSessionRepository {
  final inputs = <List<String>>[];
  @override
  Future<Result<void>> sendInput(String sessionId, String text) async {
    inputs.add([sessionId, text]);
    return const Result.ok(null);
  }

  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async =>
      const Result.ok(null);
}

class _Slow extends _Recording {
  final done = Completer<void>();
  @override
  Future<Result<void>> sendInput(String sessionId, String text) async {
    await done.future;
    return super.sendInput(sessionId, text);
  }
}

Session _s({
  String status = 'idle',
  Map<String, dynamic>? interaction,
  Map<String, dynamic>? summary,
  String? since,
  List<Map<String, dynamic>>? subagents,
}) =>
    Session.fromJson({
      'id': 'n:%1',
      'agent': 'claude',
      'status': status,
      'source': 'hooked',
      'frontend': 'tmux',
      'input_mode': 'pane',
      'tmux': {'pane_id': '%1'},
      'interaction': ?interaction,
      'summary': ?summary,
      'status_since': ?since,
      'subagents': ?subagents,
    });

Future<void> _pump(WidgetTester t, Session s, SessionRepository repo,
        {ProviderContainer? container, String? notice}) =>
    t.pumpWidget(UncontrolledProviderScope(
      container: container ??
          ProviderContainer(
              overrides: [sessionRepositoryProvider.overrideWithValue(repo)]),
      child: MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.bottomCenter,
            child: SessionComposer(session: s, inputNotice: notice),
          ),
        ),
      ),
    ));

void main() {
  test('labels', () {
    expect(
        interactionLabel(
            Interaction.fromJson({'kind': 'permission', 'tool_name': 'Bash'})),
        'Permission: Bash');
    expect(interactionLabel(Interaction.fromJson({'kind': 'plan'})),
        'Plan review');
    expect(interactionLabel(Interaction.fromJson({'kind': 'question'})),
        'Question');
    expect(respondElsewhereLabel(FrontendKind.vscode), 'Respond in VSCode');
    expect(respondElsewhereLabel(FrontendKind.external),
        'Respond in your terminal');
  });

  test('status and model lines', () {
    final now = DateTime.utc(2026, 10, 15, 9, 3, 4);
    expect(
        composerStatus(
            _s(status: 'working', since: '2026-10-15T09:00:00Z'), now),
        'Working… 3m 04s');
    expect(composerStatus(_s(status: 'working'), now), 'Working…');
    expect(composerStatus(_s(status: 'starting'), now), 'Starting…');
    expect(composerStatus(_s(), now), isNull);
    expect(
        composerStatus(
            _s(status: 'awaiting_input', interaction: {'kind': 'question'}),
            now),
        'Question');
    expect(modelLine(const Summary(modelName: 'Opus 5.5', effort: 'high')),
        'Opus 5.5 · high');
    expect(modelLine(const Summary(modelName: 'Opus 5.5')), 'Opus 5.5');
    expect(modelLine(null), isNull);
    expect(formatElapsed(const Duration(seconds: 12)), '12s');
    expect(formatElapsed(const Duration(hours: 1, minutes: 2, seconds: 9)),
        '1h 02m');
  });

  testWidgets('idle: typing and Send sends and clears', (t) async {
    final repo = _Recording();
    await _pump(t, _s(), repo);
    await t.enterText(find.byType(TextField), 'next task');
    await t.pump();
    await t.tap(find.byTooltip('Send'));
    await t.pumpAndSettle();
    expect(repo.inputs.single, ['n:%1', 'next task']);
    expect(find.text('next task'), findsNothing);
  });

  testWidgets('working: drafting allowed, Send disabled with a reason',
      (t) async {
    final repo = _Recording();
    await _pump(t, _s(status: 'working', since: '2026-10-15T09:00:00Z'), repo);
    await t.enterText(find.byType(TextField), 'later');
    await t.pump();
    final send =
        t.widget<IconButton>(find.widgetWithIcon(IconButton, Icons.send));
    expect(send.onPressed, isNull);
    // A tooltip, not a helper line that would push the input up.
    expect(find.text('Wait for idle to send'), findsNothing);
    expect(find.byTooltip('Wait for idle to send'), findsOneWidget);
    final field = t.getRect(find.byType(TextField));
    final button = t.getRect(find.widgetWithIcon(IconButton, Icons.send));
    expect(field.top, button.top, reason: 'the input and Send line up');
    expect(field.bottom, button.bottom, reason: 'the input and Send line up');
    await t.tap(find.widgetWithIcon(IconButton, Icons.send));
    await t.pump(const Duration(milliseconds: 500));
    expect(find.text('Wait for idle to send'), findsOneWidget,
        reason: 'a tap on the disabled button shows why');
    await t.pump(const Duration(seconds: 3)); // let the tooltip go
    expect(find.textContaining('Working…'), findsOneWidget);
    final width = t.getSize(find.byType(SessionComposer)).width;
    expect(t.getTopRight(find.textContaining('Working…')).dx,
        greaterThan(width * 0.75),
        reason: 'the status sits at the right end of its rule');
    await t.pumpWidget(const SizedBox()); // stop the timer
  });

  testWidgets('starting: Send disabled with a reason', (t) async {
    await _pump(t, _s(status: 'starting'), _Recording());
    await t.enterText(find.byType(TextField), 'early');
    await t.pump();
    final send =
        t.widget<IconButton>(find.widgetWithIcon(IconButton, Icons.send));
    expect(send.onPressed, isNull);
    expect(find.byTooltip('Wait for idle to send'), findsOneWidget);
    expect(find.text('Starting…'), findsOneWidget);
  });

  testWidgets('an old node renders Working… with no time and no effort',
      (t) async {
    await _pump(t, _s(status: 'working', summary: {'model_name': 'Opus 5.5'}),
        _Recording());
    expect(find.text('Working…'), findsOneWidget);
    expect(find.text('Opus 5.5'), findsOneWidget);
    await t.pumpWidget(const SizedBox());
  });

  testWidgets('a prompt replaces the input, collapses, and the draft returns',
      (t) async {
    final repo = _Recording();
    final container = ProviderContainer(
        overrides: [sessionRepositoryProvider.overrideWithValue(repo)]);
    addTearDown(container.dispose);
    await _pump(t, _s(), repo, container: container);
    await t.enterText(find.byType(TextField), 'draft');
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'permission',
          'tool_name': 'Bash'
        }),
        repo,
        container: container);
    await t.pump();
    expect(find.byType(InteractionPanel), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
    Color? labelColor() =>
        t.widget<Text>(find.text('Permission: Bash')).style?.color;
    expect(labelColor(), AppColors.dim);
    await t.tap(find.byTooltip('Collapse'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsNothing);
    expect(labelColor(), statusColor(SessionStatus.awaitingInput));
    await t.tap(find.byTooltip('Expand'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsOneWidget);
    await t.tap(find.byTooltip('Collapse'));
    await t.pump();
    await t.tap(find.text('Permission: Bash'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsOneWidget);
    await t.tap(find.text('Permission: Bash'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsOneWidget);
    await _pump(t, _s(), repo, container: container);
    await t.pump();
    expect(find.text('draft'), findsOneWidget);
  });

  testWidgets('a notice replaces the input', (t) async {
    await _pump(t, _s(), _Recording(), notice: 'Respond in VSCode');
    expect(find.text('Respond in VSCode'), findsOneWidget);
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('model line shows model and effort', (t) async {
    await _pump(
        t,
        _s(summary: {'model_name': 'Opus 5.5', 'effort': 'high'}),
        _Recording());
    expect(find.text('Opus 5.5 · high'), findsOneWidget);
  });

  Map<String, dynamic> question(String text) => {
        'kind': 'question',
        'questions': [
          {
            'question': text,
            'options': ['A', 'B'],
          }
        ],
      };

  testWidgets('a new question starts with nothing selected', (t) async {
    final repo = _Recording();
    final container = ProviderContainer(
        overrides: [sessionRepositoryProvider.overrideWithValue(repo)]);
    addTearDown(container.dispose);
    await _pump(t, _s(status: 'awaiting_input', interaction: question('One')),
        repo,
        container: container);
    await t.pump();
    await t.tap(find.text('A'));
    await t.pump();
    FilledButton submit() =>
        t.widget<FilledButton>(find.widgetWithText(FilledButton, 'Submit'));
    expect(submit().onPressed, isNotNull);
    await _pump(t, _s(status: 'awaiting_input', interaction: question('Two')),
        repo,
        container: container);
    await t.pump();
    expect(find.text('Two'), findsOneWidget);
    expect(submit().onPressed, isNull);
  });

  testWidgets('collapsing keeps the answers so far', (t) async {
    await _pump(t, _s(status: 'awaiting_input', interaction: question('One')),
        _Recording());
    await t.pump();
    await t.tap(find.text('A'));
    await t.pump();
    FilledButton submit() =>
        t.widget<FilledButton>(find.widgetWithText(FilledButton, 'Submit'));
    expect(submit().onPressed, isNotNull);
    await t.tap(find.byTooltip('Collapse'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsNothing);
    await t.tap(find.byTooltip('Expand'));
    await t.pump();
    expect(submit().onPressed, isNotNull);
  });

  testWidgets('collapsing a prompt being typed in closes the keyboard',
      (t) async {
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'permission',
          'tool_name': 'Bash'
        }),
        _Recording());
    await t.pump();
    await t.tap(find.text('Deny'));
    await t.pump();
    expect(t.testTextInput.isVisible, isTrue);
    await t.tap(find.byTooltip('Collapse'));
    await t.pump();
    expect(t.testTextInput.isVisible, isFalse);
    await t.tap(find.byTooltip('Expand'));
    await t.pump();
    expect(find.byType(TextField), findsOneWidget);
  });

  testWidgets('a new permission opens with its options, not deny mode',
      (t) async {
    final repo = _Recording();
    final container = ProviderContainer(
        overrides: [sessionRepositoryProvider.overrideWithValue(repo)]);
    addTearDown(container.dispose);
    Session perm(String tool) => _s(
        status: 'awaiting_input',
        interaction: {'kind': 'permission', 'tool_name': tool});
    await _pump(t, perm('Bash'), repo, container: container);
    await t.pump();
    await t.tap(find.text('Deny'));
    await t.pump();
    expect(find.byType(TextField), findsOneWidget);
    await _pump(t, perm('Edit'), repo, container: container);
    await t.pump();
    expect(find.byType(TextField), findsNothing);
    expect(find.text('Allow'), findsOneWidget);
  });

  testWidgets('a tall plan with the keyboard up does not overflow', (t) async {
    t.view.physicalSize = const Size(400, 800);
    t.view.devicePixelRatio = 1;
    t.view.viewInsets = const FakeViewPadding(bottom: 300);
    addTearDown(t.view.reset);
    final plan = List.generate(100, (i) => 'Step $i').join('\n\n');
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'plan',
          'plan': plan,
        }),
        _Recording());
    await t.pump();
    await t.tap(find.text('Deny'));
    await t.pump();
    expect(find.byType(TextField), findsOneWidget);
    expect(t.takeException(), isNull);
  });

  testWidgets('dragging the status line resizes and collapses the prompt',
      (t) async {
    t.view.physicalSize = const Size(400, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final plan = List.generate(100, (i) => 'Step $i').join('\n\n');
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'plan',
          'plan': plan,
        }),
        _Recording());
    await t.pump();
    final status = find.text('Plan review').first;
    double panel() => t.getSize(find.byType(InteractionPanel)).height;
    expect(panel(), lessThanOrEqualTo(800 * 0.6));
    await t.drag(status, const Offset(0, -800));
    await t.pump();
    expect(t.getSize(find.byType(SessionComposer)).height, 800);
    expect(t.takeException(), isNull);
    await t.drag(status, const Offset(0, -200));
    await t.pump();
    await t.drag(status, const Offset(0, 200));
    await t.pump();
    expect(panel(), lessThan(800 - 56 - 100));
    await t.drag(status, const Offset(0, 800));
    await t.pump();
    expect(find.byType(InteractionPanel), findsNothing);
    await t.tap(find.byTooltip('Expand'));
    await t.pump();
    expect(panel(), closeTo(800 * 0.6, 1));
  });

  testWidgets('a prompt that changes mid-drag stays open', (t) async {
    t.view.physicalSize = const Size(400, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final repo = _Recording();
    final container = ProviderContainer(
        overrides: [sessionRepositoryProvider.overrideWithValue(repo)]);
    addTearDown(container.dispose);
    await _pump(t, _s(status: 'awaiting_input', interaction: question('One')),
        repo,
        container: container);
    await t.pump();
    final gesture =
        await t.startGesture(t.getCenter(find.text('Question').first));
    for (var i = 0; i < 3; i++) {
      await gesture.moveBy(const Offset(0, -10));
      await t.pump();
    }
    await _pump(t, _s(status: 'awaiting_input', interaction: question('Two')),
        repo,
        container: container);
    await t.pump();
    await gesture.moveBy(const Offset(0, -5));
    await t.pump();
    await gesture.up();
    expect(find.byType(InteractionPanel), findsOneWidget);
    expect(find.text('Two'), findsOneWidget);
  });

  testWidgets('a prompt never shrinks below its buttons', (t) async {
    t.view.physicalSize = const Size(400, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final long = List.generate(60, (i) => 'Line $i').join('\n');
    await _pump(t, _s(status: 'awaiting_input', interaction: question(long)),
        _Recording());
    await t.pump();
    final status = find.text('Question').first;
    final gesture = await t.startGesture(t.getCenter(status));
    for (var y = 0; y < 80; y++) {
      await gesture.moveBy(const Offset(0, 10));
      await t.pump();
      expect(t.takeException(), isNull);
      if (find.byType(InteractionPanel).evaluate().isEmpty) break;
      expect(find.text('Chat about this').hitTestable(), findsOneWidget);
    }
    await gesture.up();
    expect(find.byType(InteractionPanel), findsNothing);

    t.view.physicalSize = const Size(400, 260);
    await _pump(t, _s(status: 'awaiting_input', interaction: question(long)),
        _Recording());
    await t.pump();
    expect(t.takeException(), isNull);
  });

  testWidgets('long rule labels truncate on a narrow screen', (t) async {
    t.view.physicalSize = const Size(320, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'permission',
          'tool_name': 'mcp__plugin_atlassian_atlassian__createConfluenceContent',
        }, summary: {
          'model_name': 'Claude Opus 5.5 with a very long display name indeed',
          'effort': 'high',
        }),
        _Recording());
    await t.pump();
    expect(t.takeException(), isNull);
    expect(find.textContaining('Permission:'), findsOneWidget);
    expect(find.textContaining('Claude Opus'), findsOneWidget);
  });

  testWidgets('text typed while a send is in flight is kept', (t) async {
    final repo = _Slow();
    await _pump(t, _s(), repo);
    await t.enterText(find.byType(TextField), 'first');
    await t.pump();
    await t.tap(find.byTooltip('Send'));
    await t.pump();
    await t.enterText(find.byType(TextField), 'second');
    repo.done.complete();
    await t.pumpAndSettle();
    expect(find.text('second'), findsOneWidget);
  });

  group('running subagents', () {
    final two = [
      {'id': 'a1', 'type': 'Explore', 'desc': 'find it'},
      {'id': 'a2', 'name': 'scout', 'desc': 'map it'},
    ];
    final summary = {'model_name': 'Opus 5.5', 'effort': 'high'};

    testWidgets('the model line shows the count at its left', (t) async {
      await _pump(t, _s(subagents: two, summary: summary), _Recording());
      expect(find.byIcon(Icons.smart_toy_outlined), findsOneWidget);
      expect(find.text('2'), findsOneWidget);
      expect(find.text('Opus 5.5 · high'), findsOneWidget);
      expect(t.getTopLeft(find.byIcon(Icons.smart_toy_outlined)).dx,
          lessThan(t.getTopLeft(find.text('Opus 5.5 · high')).dx));
    });

    testWidgets('none shows no icon', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      expect(find.byIcon(Icons.smart_toy_outlined), findsNothing);
    });

    testWidgets('the count leaves the composer height alone', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      final without = t.getSize(find.byType(SessionComposer)).height;
      await _pump(t, _s(subagents: two, summary: summary), _Recording());
      expect(t.getSize(find.byType(SessionComposer)).height, without);
    });

    testWidgets('the count is a 48 px wide tap target', (t) async {
      await _pump(t, _s(subagents: two, summary: summary), _Recording());
      final size = t.getSize(find
          .ancestor(
              of: find.byIcon(Icons.smart_toy_outlined),
              matching: find.byType(InkWell))
          .first);
      expect(size.width, greaterThanOrEqualTo(48));
    });

    testWidgets('tapping the count opens the sheet', (t) async {
      final container = ProviderContainer(overrides: [
        sessionRepositoryProvider.overrideWithValue(_Recording()),
      ]);
      addTearDown(container.dispose);
      final s = _s(subagents: two, summary: summary);
      container.read(sessionsProvider.notifier).put(s);
      await _pump(t, s, _Recording(), container: container);
      await t.tap(find.byIcon(Icons.smart_toy_outlined));
      await t.pumpAndSettle();
      expect(find.text('Explore'), findsOneWidget);
      expect(find.text('scout'), findsOneWidget);
    });
  });
}
