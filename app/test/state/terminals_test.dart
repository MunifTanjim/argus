import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/terminals.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _t(String node, String id, {String name = ''}) => {
      'id': '$node:$id',
      'name': name,
      'cwd': '~',
      'command': 'zsh',
      'node_id': node,
      'node_label': '$node-box',
    };

void main() {
  test('parses a terminal and titles it by name, then command', () {
    final t = NodeTerminal.fromJson(_t('A', '@1'));
    expect(t.id, 'A:@1');
    expect(t.nodeLabel, 'A-box');
    expect(t.title, 'zsh');
    expect(NodeTerminal.fromJson(_t('A', '@1', name: 'build')).title, 'build');
  });

  test('load keeps a failed node\'s previous terminals in node order', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(terminalsProvider.notifier);
    await n.load(FakeGatewayClient((m, _) async => {
          'terminals': [_t('A', '@1'), _t('B', '@1')],
        }));
    await n.load(FakeGatewayClient((m, _) async => {
          'terminals': [_t('B', '@2')],
          'failed_nodes': ['A'],
        }));
    final s = c.read(terminalsProvider);
    expect(s.terminals.map((t) => t.id), ['A:@1', 'B:@2']);
    expect(s.loaded, isTrue);
    expect(s.error, isNull);
  });

  test('a failed load keeps the list and records the error', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(terminalsProvider.notifier);
    await n.load(FakeGatewayClient((m, _) async => {'terminals': [_t('A', '@1')]}));
    await n.load(FakeGatewayClient((m, _) async => throw StateError('down')));
    final s = c.read(terminalsProvider);
    expect(s.terminals.single.id, 'A:@1');
    expect(s.error, contains('down'));
  });
}
