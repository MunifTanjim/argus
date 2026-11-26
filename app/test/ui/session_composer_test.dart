import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/voice.dart';
import 'package:argus/ui/interaction_panel.dart';
import 'package:argus/ui/prompt_completion.dart';
import 'package:argus/ui/session_composer.dart';
import 'package:argus/ui/theme.dart';
import '../support/fake_session_repository.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

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

class _Failing extends _Recording {
  @override
  Future<Result<void>> sendInput(String sessionId, String text) async =>
      Result.error(Exception('offline'));
}

class _Completing extends _Recording {
  _Completing(this.items);
  List<PromptCompletion> items;
  final calls = <List<String>>[];
  @override
  Future<Result<List<PromptCompletion>>> completePrompt(
      String sessionId, String trigger) async {
    calls.add([sessionId, trigger]);
    return Result.ok(items);
  }
}

class _GatedCompleting extends _Recording {
  final calls = <List<String>>[];
  final pending = <Completer<Result<List<PromptCompletion>>>>[];
  @override
  Future<Result<List<PromptCompletion>>> completePrompt(
      String sessionId, String trigger) {
    calls.add([sessionId, trigger]);
    final c = Completer<Result<List<PromptCompletion>>>();
    pending.add(c);
    return c.future;
  }
}

const _compact = PromptCompletion(
    name: 'compact', description: 'Compact the conversation');

BoxDecoration _box(WidgetTester t) => t
    .widget<DecoratedBox>(find.byKey(const Key('composer-box')))
    .decoration as BoxDecoration;

String _field(WidgetTester t) =>
    t.widget<TextField>(find.byType(TextField)).controller!.text;

Session _s({
  String agent = 'claude',
  String status = 'idle',
  Map<String, dynamic>? interaction,
  Map<String, dynamic>? summary,
  String? since,
  List<Map<String, dynamic>>? subagents,
  List<Map<String, dynamic>>? shells,
  bool? canPrompt,
}) =>
    Session.fromJson({
      'id': 'n:%1',
      'agent': agent,
      'status': status,
      'source': 'hooked',
      'frontend': 'tmux',
      'input_mode': 'pane',
      'tmux': {'pane_id': '%1'},
      'interaction': ?interaction,
      'summary': ?summary,
      'status_since': ?since,
      'subagents': ?subagents,
      'shells': ?shells,
      // Default: what the node sends.
      'can_prompt': canPrompt ??
          (status != 'working' &&
              status != 'starting' &&
              (interaction == null || interaction['kind'] == 'idle')),
    });

Future<void> _pump(WidgetTester t, Session s, SessionRepository repo,
        {ProviderContainer? container,
        String? notice,
        List<QueuedPrompt> queue = const [],
        VoidCallback? onSent}) =>
    t.pumpWidget(UncontrolledProviderScope(
      container: container ??
          ProviderContainer(
              overrides: [sessionRepositoryProvider.overrideWithValue(repo)]),
      child: MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.bottomCenter,
            child: SessionComposer(
                session: s, inputNotice: notice, queue: queue, onSent: onSent),
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
    String line(Summary? s) {
      final parts = modelParts(s, ArgusTokens.fallback);
      return modelLine(parts, parts.length, ArgusTokens.fallback).toPlainText();
    }

    expect(line(const Summary(modelName: 'Opus 5.5', effort: 'high')),
        'Opus 5.5 · high');
    expect(line(const Summary(modelName: 'Opus 5.5')), 'Opus 5.5');
    expect(line(null), isEmpty);
    expect(
        line(const Summary(
            modelName: 'Opus 5.5',
            effort: 'high',
            hasContext: true,
            contextPct: 42.5,
            tokens: 84000,
            hasCost: true,
            costUsd: 1.234)),
        'Opus 5.5 · high · 84.0k (43%) · \$1.23');
    expect(line(const Summary(tokens: 1200000, hasCost: true, costUsd: 0.5)),
        '1.2M · \$0.50');
    expect(line(const Summary(hasContext: true, contextPct: 42)), '42%');
    expect(formatElapsed(const Duration(seconds: 12)), '12s');
    expect(formatElapsed(const Duration(hours: 1, minutes: 2, seconds: 9)),
        '1h 02m');
  });

  testWidgets('idle: typing and Send sends and clears', (t) async {
    final repo = _Recording();
    await _pump(t, _s(), repo);
    await t.enterText(find.byType(TextField), 'next task');
    // Let the box grow to show Send: one frame starts the animation.
    await t.pump();
    await t.pump(const Duration(milliseconds: 200));
    await t.tap(find.byTooltip('Send'));
    await t.pumpAndSettle();
    expect(repo.inputs.single, ['n:%1', 'next task']);
    expect(find.text('next task'), findsNothing);
  });

  testWidgets(
    'the empty box is even, and typing keeps the space above the text',
    (t) async {
      await _pump(t, _s(), _Recording());
      double gap() =>
          t.getTopLeft(find.byType(EditableText)).dy -
          t.getTopLeft(find.byKey(const Key('composer-box'))).dy;
      final empty = gap();
      // Empty, the text sits in the middle of the box.
      expect(
        t.getBottomLeft(find.byKey(const Key('composer-box'))).dy -
            t.getBottomLeft(find.byType(EditableText)).dy,
        empty,
      );
      await t.enterText(find.byType(TextField), 'next task');
      await t.pumpAndSettle();
      expect(gap(), empty);
    },
  );


  testWidgets('the model chip has even space to its left, top, and bottom', (
    t,
  ) async {
    await _pump(
      t,
      _s(summary: {'model_name': 'Opus 4.8', 'effort': 'high'}),
      _Recording(),
    );
    await t.enterText(find.byType(TextField), 'next task');
    await t.pumpAndSettle();
    final box = t.getRect(find.byKey(const Key('composer-box')));
    final chip = t.getRect(find.byKey(const Key('model-chip')));
    final text = t.getRect(find.byType(EditableText));
    final left = chip.left - box.left;
    expect([chip.top - text.bottom, box.bottom - chip.bottom], [left, left]);
  });

  testWidgets('a sent prompt reports onSent', (t) async {
    var sent = 0;
    await _pump(t, _s(), _Recording(), onSent: () => sent++);
    await t.enterText(find.byType(TextField), 'next task');
    // Let the box grow to show Send: one frame starts the animation.
    await t.pump();
    await t.pump(const Duration(milliseconds: 200));
    await t.tap(find.byTooltip('Send'));
    await t.pumpAndSettle();
    expect(sent, 1);
  });

  testWidgets('a failed send does not report onSent', (t) async {
    var sent = 0;
    await _pump(t, _s(), _Failing(), onSent: () => sent++);
    await t.enterText(find.byType(TextField), 'next task');
    // Let the box grow to show Send: one frame starts the animation.
    await t.pump();
    await t.pump(const Duration(milliseconds: 200));
    await t.tap(find.byTooltip('Send'));
    await t.pumpAndSettle();
    expect(sent, 0);
    expect(find.textContaining('Failed:'), findsOneWidget);
  });

  testWidgets('working: drafting allowed, Send disabled with a reason',
      (t) async {
    final repo = _Recording();
    await _pump(t, _s(status: 'working', since: '2026-10-15T09:00:00Z'), repo);
    await t.enterText(find.byType(TextField), 'later');
    // Let the box grow to show Send: one frame starts the animation.
    await t.pump();
    await t.pump(const Duration(milliseconds: 200));
    final send = t.widget<IconButton>(
        find.widgetWithIcon(IconButton, Icons.arrow_upward));
    expect(send.onPressed, isNull);
    // A tooltip, not a helper line that would push the input up.
    expect(find.text('Wait for idle to send'), findsNothing);
    expect(find.byTooltip('Wait for idle to send'), findsOneWidget);
    final field = t.getRect(find.byType(TextField));
    final button =
        t.getRect(find.widgetWithIcon(IconButton, Icons.arrow_upward));
    expect(button.top, greaterThanOrEqualTo(field.bottom),
        reason: 'Send sits in the toolbar below the input');
    await t.tap(find.widgetWithIcon(IconButton, Icons.arrow_upward));
    await t.pump(const Duration(milliseconds: 500));
    expect(find.text('Wait for idle to send'), findsOneWidget,
        reason: 'a tap on the disabled button shows why');
    await t.pump(const Duration(seconds: 3)); // let the tooltip go
    expect(
        find.descendant(
            of: find.byKey(const Key('composer-status')),
            matching: find.textContaining('Working…')),
        findsOneWidget);
    await t.pumpWidget(const SizedBox()); // stop the timer
  });

  testWidgets('starting: Send disabled with a reason', (t) async {
    await _pump(t, _s(status: 'starting'), _Recording());
    await t.enterText(find.byType(TextField), 'early');
    await t.pump();
    final send = t.widget<IconButton>(
        find.widgetWithIcon(IconButton, Icons.arrow_upward));
    expect(send.onPressed, isNull);
    expect(find.byTooltip('Wait for idle to send'), findsOneWidget);
    expect(find.text('Starting…'), findsOneWidget);
  });

  testWidgets('an old node renders Working… with no time and no effort',
      (t) async {
    await _pump(t, _s(status: 'working', summary: {'model_name': 'Opus 5.5'}),
        _Recording());
    await t.enterText(find.byType(TextField), 'later');
    await t.pump();
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
    Color? border() => (_box(t).border as Border?)?.top.color;
    Color? strip() =>
        t.widget<Container>(find.byKey(const Key('composer-status'))).color;
    Color? iconColor() => t
        .widget<IconButton>(find.widgetWithIcon(IconButton, Icons.expand_more))
        .color;
    expect(labelColor(), ArgusTokens.fallback.blocked);
    expect(iconColor(), ArgusTokens.fallback.blocked);
    expect(border(), ArgusTokens.fallback.blocked);
    expect(strip(), isNot(ArgusTokens.fallback.blockedSurface));
    await t.tap(find.byTooltip('Collapse'));
    await t.pump();
    expect(find.byType(InteractionPanel), findsNothing);
    expect(labelColor(), ArgusTokens.fallback.blocked);
    expect(border(), ArgusTokens.fallback.blockedBorder);
    expect(strip(), ArgusTokens.fallback.blockedSurface);
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
    await t.enterText(find.byType(TextField), 'hi');
    await t.pump();
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

  testWidgets('on a narrow screen, a long status truncates and a long model drops',
      (t) async {
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
    expect(find.textContaining('Claude Opus'), findsNothing);
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

  testWidgets('the model chip drops parts in the TUI order, never truncating',
      (t) async {
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final s = _s(summary: {
      'model_name': 'Opus 5.5',
      'effort': 'high',
      'has_context': true,
      'context_pct': 42,
      'tokens': 84000,
      'has_cost': true,
      'cost_usd': 1.234,
    });
    const steps = [
      'Opus 5.5 · high · 84.0k (42%) · \$1.23',
      'Opus 5.5 · high · 84.0k (42%)',
      'Opus 5.5 · high',
      'Opus 5.5',
      null,
    ];
    final seen = <int>[];
    for (var w = 800.0; w >= 200; w -= 4) {
      t.view.physicalSize = Size(w, 800);
      await _pump(t, s, _Recording());
      await t.enterText(find.byType(TextField), 'hi');
      await t.pump();
      final i = steps.indexWhere((st) => st == null
          ? find.textContaining('Opus').evaluate().isEmpty
          : find.text(st).evaluate().isNotEmpty);
      expect(i, isNot(-1), reason: 'width $w');
      if (steps[i] != null) {
        final p = t.renderObject<RenderParagraph>(find.text(steps[i]!));
        expect(p.didExceedMaxLines, isFalse, reason: 'width $w');
      }
      expect(t.takeException(), isNull, reason: 'width $w');
      if (seen.isEmpty || seen.last != i) seen.add(i);
    }
    expect(seen.first, 0);
    expect(seen, [for (var i = 0; i < seen.length; i++) i]);
    expect(seen.length, greaterThanOrEqualTo(4));
  });

  testWidgets('a queueing session sends while working', (t) async {
    final repo = _Recording();
    await _pump(t, _s(status: 'working', canPrompt: true), repo);
    await t.enterText(find.byType(TextField), 'steer');
    // Let the box grow to show Send: one frame starts the animation.
    await t.pump();
    await t.pump(const Duration(milliseconds: 200));
    await t.tap(find.byIcon(Icons.arrow_upward));
    await t.pump();
    expect(repo.inputs, [
      ['n:%1', 'steer']
    ]);
  });

  testWidgets('queued prompts list above the input, at most three', (t) async {
    final q = ['a', 'b', 'c', 'd', 'e'].map((s) => QueuedPrompt(text: s)).toList();
    await _pump(t, _s(status: 'working', canPrompt: true), _Recording(),
        queue: q);
    expect(find.text('a'), findsOneWidget);
    expect(find.text('c'), findsOneWidget);
    expect(find.text('d'), findsNothing);
    expect(find.text('+2 more'), findsOneWidget);
  });

  group('prompt menu', () {
    testWidgets('a tapped row completes the prompt without sending',
        (t) async {
      final repo = _Completing([_compact]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(repo.calls, [
        ['n:%1', '/']
      ]);
      expect(find.text('Compact the conversation'), findsOneWidget);
      await t.tap(find.text('compact'));
      await t.pumpAndSettle();
      expect(_field(t), '/compact ');
      expect(find.text('Compact the conversation'), findsNothing);
      expect(repo.inputs, isEmpty);
    });

    testWidgets('Enter selects the first row while the menu is open',
        (t) async {
      final repo = _Completing([
        _compact,
        const PromptCompletion(name: 'clear', description: ''),
      ]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      await t.sendKeyEvent(LogicalKeyboardKey.enter);
      await t.pumpAndSettle();
      expect(_field(t), '/compact ');
      expect(repo.inputs, isEmpty);
    });

    testWidgets('an empty result shows no menu and is fetched again',
        (t) async {
      final repo = _Completing([]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(find.text('compact'), findsNothing);
      await t.enterText(find.byType(TextField), '');
      await t.pump();
      repo.items = [_compact];
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(repo.calls, hasLength(2));
      expect(find.text('compact'), findsOneWidget);
    });

    testWidgets('a send drops the cached items', (t) async {
      final repo = _Completing([_compact]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      await t.enterText(find.byType(TextField), '');
      await t.pump();
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(repo.calls, hasLength(1), reason: 'the items are cached');
      await t.enterText(find.byType(TextField), 'hi');
      await t.pump();
      await t.tap(find.byTooltip('Send'));
      await t.pumpAndSettle();
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(repo.calls, hasLength(2));
    });

    testWidgets('the menu hides while the field has no focus', (t) async {
      final repo = _Completing([_compact]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      expect(find.text('compact'), findsOneWidget);
      FocusManager.instance.primaryFocus!.unfocus();
      await t.pumpAndSettle();
      expect(find.text('compact'), findsNothing);
      await t.tap(find.byType(TextField));
      await t.pumpAndSettle();
      expect(find.text('compact'), findsOneWidget);
    });

    testWidgets('a mouse click on a row completes the prompt', (t) async {
      final repo = _Completing([_compact]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      final click = await t.startGesture(t.getCenter(find.text('compact')),
          kind: PointerDeviceKind.mouse);
      await t.pump();
      await click.up();
      await t.pumpAndSettle();
      expect(_field(t), '/compact ');
    }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

    testWidgets('a fetch in flight during a send is not cached', (t) async {
      final repo = _GatedCompleting();
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), '/');
      await t.pump();
      expect(repo.calls, hasLength(1));
      await t.pump(const Duration(milliseconds: 200)); // the box grows
      await t.tap(find.byTooltip('Send'));
      await t.pump();
      repo.pending.single.complete(const Result.ok([_compact]));
      await t.pumpAndSettle();
      expect(repo.inputs, [
        ['n:%1', '/']
      ]);
      await t.enterText(find.byType(TextField), '/');
      await t.pump();
      expect(repo.calls, hasLength(2), reason: 'the stale fetch was cached');
    });

    testWidgets('the menu fits in the space above the field', (t) async {
      final repo = _Completing([
        for (var i = 0; i < 6; i++)
          PromptCompletion(name: 'cmd$i', description: ''),
      ]);
      await t.pumpWidget(UncontrolledProviderScope(
        container: ProviderContainer(
            overrides: [sessionRepositoryProvider.overrideWithValue(repo)]),
        child: MaterialApp(
          home: Scaffold(
            body: Padding(
              padding: const EdgeInsets.only(top: 100),
              child: Align(
                alignment: Alignment.topCenter,
                child: SessionComposer(session: _s()),
              ),
            ),
          ),
        ),
      ));
      await t.enterText(find.byType(TextField), '/');
      await t.pumpAndSettle();
      final menu = t.getRect(find.byType(PromptMenu));
      final field = t.getRect(find.byType(TextField));
      expect(menu.height, greaterThan(0));
      expect(menu.top, greaterThanOrEqualTo(0));
      expect(menu.bottom, lessThanOrEqualTo(field.top));
    });

    testWidgets('Enter with the menu closed neither selects nor sends',
        (t) async {
      final repo = _Completing([_compact]);
      await _pump(t, _s(), repo);
      await t.enterText(find.byType(TextField), 'hello');
      await t.pumpAndSettle();
      await t.sendKeyEvent(LogicalKeyboardKey.enter);
      await t.pumpAndSettle();
      expect(_field(t), 'hello');
      expect(repo.inputs, isEmpty);
      expect(repo.calls, isEmpty);
    });
  });

  testWidgets('the composer box takes the corner radius from the theme',
      (t) async {
    const prefs = AppearancePrefs(radius: CornerRadius.rounded);
    await t.pumpWidget(ProviderScope(
      overrides: [
        sessionRepositoryProvider.overrideWithValue(FakeSessionRepository()),
      ],
      child: MaterialApp(
        theme: buildArgusTheme(prefs: prefs),
        home: Scaffold(body: SessionComposer(session: _s())),
      ),
    ));
    expect(_box(t).borderRadius, BorderRadius.circular(12 + 4));
  });

  testWidgets('the box border paints over the status strip', (t) async {
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'permission',
          'tool_name': 'Bash'
        }),
        _Recording());
    await t.pump();
    // An opaque strip painted over a background border hides its top edge.
    expect(
        t.widget<DecoratedBox>(find.byKey(const Key('composer-box'))).position,
        DecorationPosition.foreground);
  });

  testWidgets('idle shows no status strip', (t) async {
    await _pump(t, _s(), _Recording());
    expect(find.byKey(const Key('composer-status')), findsNothing);
  });

  testWidgets('working shows the status strip', (t) async {
    await _pump(
        t,
        _s(status: 'working', since: DateTime.now().toUtc().toIso8601String()),
        _Recording());
    expect(
        find.descendant(
            of: find.byKey(const Key('composer-status')),
            matching: find.textContaining('Working…')),
        findsOneWidget);
    await t.pumpWidget(const SizedBox()); // stop the timer
  });

  group('compact when empty', () {
    final summary = {'model_name': 'Opus 5.5', 'effort': 'high'};

    testWidgets('an empty input hides Send and the model chip', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      expect(find.byIcon(Icons.arrow_upward), findsNothing);
      expect(find.text('Opus 5.5 · high'), findsNothing);
      expect(find.byType(TextField), findsOneWidget);
    });

    testWidgets('typing expands to the toolbar', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      final compact = t.getSize(find.byType(SessionComposer)).height;
      await t.enterText(find.byType(TextField), 'hi');
      await t.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_upward), findsOneWidget);
      expect(find.text('Opus 5.5 · high'), findsOneWidget);
      expect(t.getSize(find.byType(SessionComposer)).height,
          greaterThan(compact));
    });

    testWidgets('focus alone does not expand', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      await t.tap(find.byType(TextField));
      await t.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_upward), findsNothing);
    });

    testWidgets('clearing the input returns to compact', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      await t.enterText(find.byType(TextField), 'hi');
      await t.pumpAndSettle();
      await t.enterText(find.byType(TextField), '');
      await t.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_upward), findsNothing);
    });

    testWidgets('a sent prompt returns to compact', (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      await t.enterText(find.byType(TextField), 'hi');
      await t.pumpAndSettle();
      await t.tap(find.byTooltip('Send'));
      await t.pumpAndSettle();
      expect(find.byIcon(Icons.arrow_upward), findsNothing);
    });

    testWidgets('the text field keeps its state when the box expands',
        (t) async {
      await _pump(t, _s(summary: summary), _Recording());
      await t.showKeyboard(find.byType(TextField));
      final before = t.state(find.byType(EditableText));
      await t.enterText(find.byType(TextField), 'h');
      await t.pumpAndSettle();
      expect(t.state(find.byType(EditableText)), same(before));
    });

    testWidgets('the status strip and the queue stay in compact', (t) async {
      await _pump(t, _s(status: 'working', canPrompt: true), _Recording(),
          queue: [const QueuedPrompt(text: 'queued one')]);
      expect(find.byKey(const Key('composer-status')), findsOneWidget);
      expect(find.text('queued one'), findsOneWidget);
      expect(find.byIcon(Icons.arrow_upward), findsNothing);
      await t.pumpWidget(const SizedBox()); // stop the timer
    });
  });

  testWidgets('the model chip drops a part only when it does not fit',
      (t) async {
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final s = _s(summary: {
      'model_name': 'Opus 5.5',
      'effort': 'high',
      'has_context': true,
      'context_pct': 42,
      'tokens': 84000,
      'has_cost': true,
      'cost_usd': 1.234,
    });
    final parts = modelParts(s.summary, ArgusTokens.fallback);
    // The style the chip draws with: the theme default merged with its own.
    TextStyle? drawn;
    double width(int n) {
      final p = TextPainter(
        text: TextSpan(
            style: drawn,
            children: [modelLine(parts, n, ArgusTokens.fallback)]),
        textDirection: TextDirection.ltr,
      )..layout();
      final w = p.width;
      p.dispose();
      return w;
    }

    for (var w = 800.0; w >= 200; w -= 4) {
      t.view.physicalSize = Size(w, 800);
      await _pump(t, s, _Recording());
      await t.enterText(find.byType(TextField), 'hi');
      await t.pump();
      await t.pump(const Duration(milliseconds: 200));
      final room = t.getSize(find.byKey(const Key('model-chip-slot'))).width;
      var shown = 0;
      for (var n = parts.length; n > 0; n--) {
        final text = modelLine(parts, n, ArgusTokens.fallback).toPlainText();
        if (find.text(text).evaluate().isNotEmpty) {
          shown = n;
          break;
        }
      }
      final chipText = find.descendant(
          of: find.byKey(const Key('model-chip')), matching: find.byType(Text));
      if (chipText.evaluate().isNotEmpty) {
        drawn = DefaultTextStyle.of(t.element(chipText))
            .style
            .merge(t.widget<Text>(chipText).style);
      }
      if (shown < parts.length && drawn != null) {
        expect(width(shown + 1) + 2 * 8 + 2, greaterThan(room),
            reason: 'width $w dropped a part that fits');
      }
    }
  });

  testWidgets('the toolbar has no trigger buttons', (t) async {
    for (final agent in ['claude', 'codex', 'opencode']) {
      await _pump(t, _s(agent: agent), _Recording());
      expect(find.byType(TextButton), findsNothing, reason: agent);
    }
  });

  testWidgets('a pending prompt hides the toolbar and the mic', (t) async {
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    final container = ProviderContainer(overrides: [
      sessionRepositoryProvider.overrideWithValue(_Recording()),
      voiceStoreProvider.overrideWithValue(store),
    ]);
    addTearDown(container.dispose);
    await _pump(t, _s(), _Recording(), container: container);
    await t.pumpAndSettle();
    expect(find.byKey(const Key('voice-input')), findsOneWidget);
    await _pump(
        t,
        _s(status: 'awaiting_input', interaction: {
          'kind': 'permission',
          'tool_name': 'Bash'
        }),
        _Recording(),
        container: container);
    await t.pump();
    expect(find.byKey(const Key('voice-input')), findsNothing);
    expect(find.byIcon(Icons.arrow_upward), findsNothing);
  });
}
