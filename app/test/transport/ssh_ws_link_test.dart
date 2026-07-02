import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/ssh_ws_link.dart';

class FakeInner implements RpcLink {
  final _c = StreamController<RpcMessage>.broadcast();
  final sent = <String>[];
  bool closed = false;
  @override
  Stream<RpcMessage> get incoming => _c.stream;
  @override
  void send(String frame) => sent.add(frame);
  @override
  Future<void> close() async {
    closed = true;
    await _c.close();
  }
}

void main() {
  test('delegates send/incoming to inner and closes both', () async {
    final inner = FakeInner();
    var tunnelClosed = false;
    final link = SshWebSocketRpcLink.raw(inner, () async {
      tunnelClosed = true;
    });

    link.send('frame1');
    expect(inner.sent, ['frame1']);

    await link.close();
    expect(inner.closed, isTrue);
    expect(tunnelClosed, isTrue);
  });
}
