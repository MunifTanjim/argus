import 'dart:async';

import 'package:argus/state/bot_link.dart';
import 'package:argus/state/bot_link_listener.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test(
    'opened bot links become the pending link; others are ignored',
    () async {
      final links = StreamController<Uri>();
      addTearDown(links.close);
      final c = ProviderContainer(
        overrides: [appLinkStreamProvider.overrideWithValue(links.stream)],
      );
      addTearDown(c.dispose);
      c.read(botLinkListenerProvider);

      links.add(Uri.parse('argus://bot/A:a1?text=status%3F'));
      await pumpEventQueue();
      expect(
        c.read(pendingBotLinkProvider),
        const BotLink('A:a1', text: 'status?'),
      );

      c.read(pendingBotLinkProvider.notifier).state = null;
      links.add(Uri.parse('argus://pair?url=x&token=y'));
      links.add(Uri.parse('https://example.com/bot/A:a1'));
      await pumpEventQueue();
      expect(c.read(pendingBotLinkProvider), isNull);

      links.add(Uri.parse('argus://bot/b2'));
      await pumpEventQueue();
      expect(c.read(pendingBotLinkProvider), const BotLink('b2'));
    },
  );

  test('a stream error does not stop later links', () async {
    final links = StreamController<Uri>();
    addTearDown(links.close);
    final got = <BotLink>[];
    final sub = listenBotLinks(links.stream, got.add);
    addTearDown(sub.cancel);

    links.addError(const FormatException('bad link'));
    links.add(Uri.parse('argus://bot/A:a1'));
    await pumpEventQueue();
    expect(got, [const BotLink('A:a1')]);
  });

  test('disposing stops listening', () async {
    final links = StreamController<Uri>.broadcast();
    addTearDown(links.close);
    final c = ProviderContainer(
      overrides: [appLinkStreamProvider.overrideWithValue(links.stream)],
    );
    c.read(botLinkListenerProvider);
    expect(links.hasListener, isTrue);
    c.dispose();
    expect(links.hasListener, isFalse);
  });
}
