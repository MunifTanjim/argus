import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/push/webpush_crypto.dart';

List<int> _b64url(String s) =>
    base64Url.decode(s + '=' * ((4 - s.length % 4) % 4));

List<int> _hex(String s) => [
      for (var i = 0; i < s.length; i += 2) int.parse(s.substring(i, i + 2), radix: 16)
    ];

void main() {
  test('decrypts a body produced by the Go encryptor', () async {
    final privateKey = _hex('e859f5e3d28955864eb9d1d9d89c091372af501a59eac3890c235e41e0eb3fe7');
    const auth = 'p3pGeayq2kyywiSY8ZqCZg';
    final body = _b64url('sGK83czgtFjdXc_IdEzhtgAAEABBBNOOFRKizonxBeK25gBG46GtLbSeeT7WM8OivsWAATYMUiJ1l_Rezde3SQ69vtR6TQAFZIJKKGeHKAAVrGjVs2f6yV8BGQuZzvrwvqJh_34INggqLVVMjqxFkjj1kl1591fav0K-xofzeqm88iFuH1kMDHENkZ5ulIU761iVHodRlQDX6IphEZCgPjalgrRqvDNeig');
    const plaintext = '{"id":"v1","title":"hi","body":"there","data":{"session_id":"s1"}}';

    final out = await decryptWebPush(privateKey: privateKey, auth: auth, body: body);
    expect(utf8.decode(out), plaintext);
  });

  test('generateWebPushKeys yields a 65-byte point and 16-byte auth', () async {
    final keys = await generateWebPushKeys();
    expect(_b64url(keys.p256dh).length, 65);
    expect(_b64url(keys.auth).length, 16);
  });
}
