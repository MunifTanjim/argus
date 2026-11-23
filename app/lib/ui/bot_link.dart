import 'package:flutter/material.dart';

import '../models/bot.dart';
import '../state/bot_link.dart';
import '../state/bots.dart';
import 'bot_screen.dart';
import 'thread_screen.dart';

/// The bot's most recently active open chat thread, or null.
BotThread? latestChatThread(Iterable<BotThread> threads) {
  BotThread? best;
  for (final t in threads) {
    if (t.kind != 'chat' || t.status != 'active') continue;
    if (best == null || t.lastActiveAt.isAfter(best.lastActiveAt)) best = t;
  }
  return best;
}

/// The gateway-qualified id of the bot [id] names: the id itself, or a
/// node-local id (a bot's own `argus bot` output) that one bot has.
/// Null when no bot, or more than one, matches.
String? resolveBotLinkId(BotsState state, String id) {
  if (state.bots.any((a) => a.id == id)) return id;
  final local = state.bots.where((a) => a.id.endsWith(':$id')).toList();
  return local.length == 1 ? local.single.id : null;
}

/// Opens [link]: the bot's latest chat thread with the composer prefilled,
/// or the bot screen with a new chat prefilled when it has none.
void openBotLink(BuildContext context, BotsState state, BotLink link) {
  final nav = Navigator.of(context, rootNavigator: true);
  final botId = resolveBotLinkId(state, link.botId);
  if (botId == null) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(content: Text('bot not found')));
    return;
  }
  final thread = latestChatThread(state.threads[botId] ?? const []);
  nav.push(
    thread == null
        ? botRoute(botId, chatText: link.text)
        : threadRoute(
            botId: botId,
            threadId: thread.id,
            title: thread.title,
            initialText: link.text,
          ),
  );
}
