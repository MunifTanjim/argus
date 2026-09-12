import 'dart:convert';

/// Unix seconds [d] from [now].
int unixIn(Duration d, {DateTime? now}) =>
    (now ?? DateTime.now()).add(d).millisecondsSinceEpoch ~/ 1000;

String pushPortSubscribeBody(String endpoint, Duration expiresIn) => jsonEncode({
      'data': {'endpoint': endpoint, 'expires_at': unixIn(expiresIn)},
    });
