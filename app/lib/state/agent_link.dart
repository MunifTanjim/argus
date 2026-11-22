import 'package:flutter_riverpod/legacy.dart';

/// A link to an agent: `argus://agent/<id>?text=<message>`. [text] is an
/// optional message to prefill.
class AgentLink {
  const AgentLink(this.agentId, {this.text = ''});

  final String agentId;
  final String text;

  @override
  bool operator ==(Object other) =>
      other is AgentLink && other.agentId == agentId && other.text == text;

  @override
  int get hashCode => Object.hash(agentId, text);
}

/// The agent id in an `argus://agent/<id>` link, or null for any other string.
AgentLink? parseAgentLink(String raw) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null || uri.scheme != 'argus' || uri.host != 'agent') return null;
  final segments = uri.pathSegments.where((s) => s.isNotEmpty).toList();
  if (segments.length != 1) return null;
  return AgentLink(segments.first, text: uri.queryParameters['text'] ?? '');
}

/// An agent link waiting for the agents to load. HomeShell opens it, then
/// clears it.
final pendingAgentLinkProvider = StateProvider<AgentLink?>((ref) => null);
