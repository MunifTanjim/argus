import 'package:argus/e2e/aggregate.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('server.* methods are server-addressed', () {
    expect(serverAddressed, containsAll(['server.checkUpgrade', 'server.upgrade', 'server.restart']));
    expect(serverAddressed.contains('server.info'), isFalse);
  });

  test('a node_id routes to the node, none to the gateway', () {
    expect(routesToNode({'node_id': 'n1'}), isTrue);
    expect(routesToNode({'node_id': ''}), isFalse);
    expect(routesToNode(const <String, dynamic>{}), isFalse);
    expect(routesToNode(null), isFalse);
  });
}
