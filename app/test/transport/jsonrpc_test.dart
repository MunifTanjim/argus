import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/jsonrpc.dart';

void main() {
  test('encodeRequest emits newline-terminated json-rpc 2.0', () {
    final frame = encodeRequest('1', 'ping');
    expect(frame.endsWith('\n'), isTrue);
    final m = jsonDecode(frame.trim()) as Map<String, dynamic>;
    expect(m['jsonrpc'], '2.0');
    expect(m['id'], '1');
    expect(m['method'], 'ping');
    expect(m.containsKey('params'), isFalse);
  });

  test('parses a response with result', () {
    final m = RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","id":"7","result":[1,2]}'));
    expect(m.isResponse, isTrue);
    expect(m.isNotification, isFalse);
    expect(m.id, '7');
    expect(m.result, [1, 2]);
  });

  test('parses a notification (no id)', () {
    final m = RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"session.event","params":{"type":"added"}}'));
    expect(m.isNotification, isTrue);
    expect(m.method, 'session.event');
  });

  test('parses an error response', () {
    final m = RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","id":"9","error":{"code":-32601,"message":"no method"}}'));
    expect(m.isResponse, isTrue);
    expect(m.error!.code, -32601);
    expect(m.error!.message, 'no method');
  });
}
