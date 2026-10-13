import 'package:argus/state/server_api.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

void main() {
  test('checkUpgrade sends node_id and parses the reply', () async {
    final c = FakeGatewayClient((m, p) async => {'current': '1.0.0', 'latest': '1.1.0'});
    final r = await ServerApi(() => c).checkUpgrade('n1');
    expect(c.calls.single.$1, 'server.checkUpgrade');
    expect(c.calls.single.$2, {'node_id': 'n1'});
    expect(r!.available, isTrue);
  });

  test('checkUpgrade on the gateway sends no node_id', () async {
    final c = FakeGatewayClient((m, p) async => {'current': '1.0.0', 'latest': '1.0.0'});
    final r = await ServerApi(() => c).checkUpgrade(null);
    expect(c.calls.single.$1, 'server.checkUpgrade');
    expect(c.calls.single.$2, <String, dynamic>{});
    expect(r!.available, isFalse);
  });

  test('checkUpgrade returns null for an old target', () async {
    final c = FakeGatewayClient((m, p) async => throw const RpcError(-32601, 'method not found'));
    expect(await ServerApi(() => c).checkUpgrade('n1'), isNull);
  });

  test('upgrade restarts only when the version changed', () async {
    var to = '1.1.0';
    final c = FakeGatewayClient((m, p) async => m == 'server.upgrade' ? {'from': '1.0.0', 'to': to} : null);
    final api = ServerApi(() => c);
    expect(await api.upgrade('n1'), ('1.0.0', '1.1.0'));
    expect(c.calls.map((e) => e.$1), ['server.upgrade', 'server.restart']);
    c.calls.clear();
    to = '1.0.0';
    await api.upgrade('n1');
    expect(c.calls.map((e) => e.$1), ['server.upgrade']);
  });

  test('a failed restart after the swap carries the new version', () async {
    final c = FakeGatewayClient((m, p) async => m == 'server.upgrade'
        ? {'from': '1.0.0', 'to': '1.1.0'}
        : throw const RpcError(-32000, 'boom'));
    await expectLater(
      ServerApi(() => c).upgrade('n1'),
      throwsA(isA<RestartFailed>()
          .having((e) => e.to, 'to', '1.1.0')
          .having((e) => e.error, 'error', isA<RpcError>())),
    );
  });

  test('a lost restart reply counts as a restart', () async {
    final c = FakeGatewayClient((m, p) async => m == 'server.upgrade'
        ? {'from': '1.0.0', 'to': '1.1.0'}
        : throw StateError('connection closed'));
    expect(await ServerApi(() => c).upgrade('n1'), ('1.0.0', '1.1.0'));
  });
}
