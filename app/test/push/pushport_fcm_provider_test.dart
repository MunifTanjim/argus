import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:http/http.dart' as http;

import 'package:argus/push/pushport_client.dart';
import 'package:argus/push/pushport_fcm_provider.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/push_message.dart';
import 'package:argus/push/webpush_crypto.dart';

List<int> _hex(String s) => [
      for (var i = 0; i < s.length; i += 2) int.parse(s.substring(i, i + 2), radix: 16)
    ];

List<int> _b64url(String s) =>
    base64Url.decode(s + '=' * ((4 - s.length % 4) % 4));

// Fixed keys from the Go cross-compat test vector.
final _fixedKeys = WebPushKeys(
  privateKey: _hex('e859f5e3d28955864eb9d1d9d89c091372af501a59eac3890c235e41e0eb3fe7'),
  auth: 'p3pGeayq2kyywiSY8ZqCZg',
  p256dh: 'TESTPUB',
);

// Ciphertext body (encrypts '{"id":"v1","title":"hi","body":"there","data":{"session_id":"s1"}}').
final _encryptedBody = _b64url(
    'sGK83czgtFjdXc_IdEzhtgAAEABBBNOOFRKizonxBeK25gBG46GtLbSeeT7WM8OivsWAATYMUiJ1l_Rezde3SQ69vtR6TQAFZIJKKGeHKAAVrGjVs2f6yV8BGQuZzvrwvqJh_34INggqLVVMjqxFkjj1kl1591fav0K-xofzeqm88iFuH1kMDHENkZ5ulIU761iVHodRlQDX6IphEZCgPjalgrRqvDNeig');

// Ciphertext body encrypting '{"id":"v2","title":"hi","body":"there","data":{"node_id":"n1","session_id":"s1"}}'.
// Used to verify the foreground FCM path composites session_id as nodeId:sessionId.
final _encryptedBodyWithNodeId = _b64url(
    '-xmAaudP6yEQA3ko6AdJiwAAEABBBOeGTy-ZT1vL5xK7HH1P89AHefYGlFI2qg2bkOBQTo00GEIk2rgOpMXtbP7BmXp4QdSGmcbBvKgPI6F3NJvwL2_Z4GyR2PtuiZZj6Z_DWmBQyzT7Cg4OemgtMOb4enhULdpe2_h_qXbR-hjEs8E1VxponSvGONCY14OIL_38xqKHZWwfc0dkjK1lyMGHksOniQgfsBMbjHVY2QMk9uNxPFwldQ');

const _sealedEndpoint = 'https://push.pushport.dev/push/fcm-sealed';

PushPortClient _mockClient() => PushPortClient(
      baseUrl: 'https://push.pushport.dev',
      appId: 'argus',
      httpClient: MockClient((_) async => http.Response(
            jsonEncode({
              'data': {'endpoint': _sealedEndpoint, 'expires_at': 999},
            }),
            200,
            headers: {'content-type': 'application/json'},
          )),
    );

PushPortFcmProvider _makeProvider(StreamController<List<int>> ctrl) {
  return PushPortFcmProvider(
    client: _mockClient(),
    getFcmToken: () async => 'test-fcm-token',
    incomingEncrypted: ctrl.stream,
    keyStore: FixedWebPushKeyStore(_fixedKeys),
  );
}

void main() {
  test('reports target and decrypts incoming message', () async {
    final ctrl = StreamController<List<int>>();
    final provider = _makeProvider(ctrl);

    PushTarget? emittedTarget;
    final messages = <PushMessage>[];

    await provider.start(
      onTarget: (t) => emittedTarget = t,
      onMessage: messages.add,
      onOpen: (_) {},
    );

    expect(emittedTarget, isNotNull);
    expect(emittedTarget!.endpoint, _sealedEndpoint);
    expect(emittedTarget!.p256dh, _fixedKeys.p256dh);
    expect(emittedTarget!.auth, _fixedKeys.auth);

    ctrl.add(_encryptedBody);
    await Future<void>.delayed(Duration.zero);

    expect(messages, hasLength(1));
    expect(messages.first.title, 'hi');
    expect(messages.first.body, 'there');

    await ctrl.close();
  });

  test('silently drops a corrupt body and keeps the subscription alive', () async {
    final ctrl = StreamController<List<int>>();
    final provider = _makeProvider(ctrl);

    final messages = <PushMessage>[];
    await provider.start(
      onTarget: (_) {},
      onMessage: messages.add,
      onOpen: (_) {},
    );

    // Garbage body — decryptWebPush throws.
    ctrl.add([0x00, 0x01, 0x02]);
    await Future<void>.delayed(Duration.zero);
    expect(messages, isEmpty);

    ctrl.add(_encryptedBody);
    await Future<void>.delayed(Duration.zero);
    expect(messages, hasLength(1));
    expect(messages.first.title, 'hi');

    await ctrl.close();
  });

  test('forwards duplicates — dedup is the display chokepoint (show), not here', () async {
    final ctrl = StreamController<List<int>>();
    final provider = _makeProvider(ctrl);

    final messages = <PushMessage>[];
    await provider.start(
      onTarget: (_) {},
      onMessage: messages.add,
      onOpen: (_) {},
    );

    ctrl.add(_encryptedBody);
    ctrl.add(_encryptedBody);
    await Future<void>.delayed(Duration.zero);

    // The provider no longer dedups; PushNotifications.show dedups by id so a
    // message shown in the background is not re-shown in the foreground.
    expect(messages, hasLength(2));
    await ctrl.close();
  });

  test('composites session_id as nodeId:sessionId when node_id is present', () async {
    final ctrl = StreamController<List<int>>();
    final provider = _makeProvider(ctrl);

    final messages = <PushMessage>[];
    await provider.start(
      onTarget: (_) {},
      onMessage: messages.add,
      onOpen: (_) {},
    );

    ctrl.add(_encryptedBodyWithNodeId);
    await Future<void>.delayed(Duration.zero);

    expect(messages, hasLength(1));
    expect(messages.first.sessionId, 'n1:s1');
  });
}
