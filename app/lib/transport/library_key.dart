/// A named OpenSSH private key in the reusable key library. [id] is the storage
/// key suffix, so it is not duplicated inside the record JSON.
class LibraryKey {
  const LibraryKey({
    required this.id,
    required this.name,
    required this.pem,
    this.passphrase,
  });

  final String id;
  final String name;
  final String pem;
  final String? passphrase;

  Map<String, dynamic> toJson() => {
        'name': name,
        'pem': pem,
        if (passphrase != null) 'passphrase': passphrase,
      };

  factory LibraryKey.fromJson(String id, Map<String, dynamic> j) => LibraryKey(
        id: id,
        name: j['name'] as String,
        pem: j['pem'] as String,
        passphrase: j['passphrase'] as String?,
      );
}
