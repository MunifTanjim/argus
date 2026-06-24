/// A decoded push payload. [data] carries the structured fields the gateway
/// attaches (session_id, node_id) so a tap can deep-link to the right session.
class PushMessage {
  const PushMessage({this.id, this.title, this.body, this.data = const {}});

  /// A per-delivery id stamped by the gateway, used to drop messages the
  /// UnifiedPush Android plugin replays to a freshly attached engine on app
  /// relaunch. Null for payloads without one (older/external senders).
  final String? id;
  final String? title;
  final String? body;
  final Map<String, String> data;

  String? get sessionId => data['session_id'];
}
