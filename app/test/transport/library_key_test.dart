// app/test/transport/library_key_test.dart
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/library_key.dart';

void main() {
  test('round-trips through json without embedding the id', () {
    const k = LibraryKey(id: 'k1', name: 'laptop', pem: 'PEM', passphrase: 'pw');
    final j = k.toJson();
    expect(j.containsKey('id'), isFalse);
    final back = LibraryKey.fromJson('k1', j);
    expect(back.id, 'k1');
    expect(back.name, 'laptop');
    expect(back.pem, 'PEM');
    expect(back.passphrase, 'pw');
  });

  test('omits passphrase when null', () {
    const k = LibraryKey(id: 'k1', name: 'n', pem: 'PEM');
    expect(k.toJson().containsKey('passphrase'), isFalse);
    expect(LibraryKey.fromJson('k1', k.toJson()).passphrase, isNull);
  });
}
