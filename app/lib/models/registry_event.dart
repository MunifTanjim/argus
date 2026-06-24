import 'session.dart';

enum RegistryEventType { added, updated, removed, unknown }

RegistryEventType _typeFromWire(String? s) {
  switch (s) {
    case 'added':
      return RegistryEventType.added;
    case 'updated':
      return RegistryEventType.updated;
    case 'removed':
      return RegistryEventType.removed;
    default:
      return RegistryEventType.unknown;
  }
}

class RegistryEvent {
  final RegistryEventType type;
  final Session session;

  const RegistryEvent({required this.type, required this.session});

  factory RegistryEvent.fromJson(Map<String, dynamic> j) => RegistryEvent(
        type: _typeFromWire(j['type'] as String?),
        session: Session.fromJson(j['session'] as Map<String, dynamic>),
      );
}
