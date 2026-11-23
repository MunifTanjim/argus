import 'package:flutter_riverpod/legacy.dart';

/// A link to a bot: `argus://bot/<id>?text=<message>`. [text] is an
/// optional message to prefill.
class BotLink {
  const BotLink(this.botId, {this.text = ''});

  final String botId;
  final String text;

  @override
  bool operator ==(Object other) =>
      other is BotLink && other.botId == botId && other.text == text;

  @override
  int get hashCode => Object.hash(botId, text);
}

/// The bot id in an `argus://bot/<id>` link, or null for any other string.
BotLink? parseBotLink(String raw) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null || uri.scheme != 'argus' || uri.host != 'bot') return null;
  final segments = uri.pathSegments.where((s) => s.isNotEmpty).toList();
  if (segments.length != 1) return null;
  return BotLink(segments.first, text: uri.queryParameters['text'] ?? '');
}

/// A bot link waiting for the bots to load. HomeShell opens it, then
/// clears it.
final pendingBotLinkProvider = StateProvider<BotLink?>((ref) => null);
