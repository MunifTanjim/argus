import 'dart:async';

import 'jsonrpc.dart';

class RpcClient {
  RpcClient({required Stream<RpcMessage> incoming, required this.sendFrame}) {
    _sub = incoming.listen(_onMessage);
  }

  final void Function(String frame) sendFrame;
  final _notifications = StreamController<RpcMessage>.broadcast();
  final _pending = <String, Completer<Object?>>{};
  late final StreamSubscription<RpcMessage> _sub;
  int _nextId = 0;
  bool _closed = false;

  Stream<RpcMessage> get notifications => _notifications.stream;

  Future<Object?> call(String method, [Object? params]) {
    if (_closed) {
      return Future.error(StateError('client closed'));
    }
    final id = (++_nextId).toString();
    final completer = Completer<Object?>();
    _pending[id] = completer;
    sendFrame(encodeRequest(id, method, params));
    return completer.future;
  }

  void _onMessage(RpcMessage m) {
    if (m.isNotification) {
      _notifications.add(m);
      return;
    }
    if (m.isResponse) {
      final c = _pending.remove(m.id);
      if (c == null) return;
      if (m.error != null) {
        c.completeError(m.error!);
      } else {
        c.complete(m.result);
      }
    }
  }

  void close([Object? error]) {
    if (_closed) return;
    _closed = true;
    _sub.cancel();
    final err = error ?? StateError('connection closed');
    for (final c in _pending.values) {
      if (!c.isCompleted) c.completeError(err);
    }
    _pending.clear();
    _notifications.close();
  }
}
