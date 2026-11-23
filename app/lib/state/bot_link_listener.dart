import 'dart:async';

import 'package:app_links/app_links.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'bot_link.dart';

/// Links the OS opens the app with: the launch link, then each later one.
final appLinkStreamProvider = Provider<Stream<Uri>>(
  (ref) => AppLinks().uriLinkStream,
);

/// Turns each opened `argus://bot/...` link into the pending bot link, which
/// HomeShell opens. Other links are ignored.
final botLinkListenerProvider = Provider<void>((ref) {
  final sub = listenBotLinks(
    ref.watch(appLinkStreamProvider),
    (link) => ref.read(pendingBotLinkProvider.notifier).state = link,
  );
  ref.onDispose(sub.cancel);
});

StreamSubscription<Uri> listenBotLinks(
  Stream<Uri> links,
  void Function(BotLink link) onLink,
) {
  return links.listen(
    (uri) {
      final link = parseBotLink(uri.toString());
      if (link != null) onLink(link);
    },
    // A malformed link must not end the subscription.
    onError: (Object _) {},
  );
}
