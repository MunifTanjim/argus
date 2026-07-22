import 'dart:typed_data';

import 'symmetric_state.dart' show blake2s;

/// The PGP biometric word list (even-index, two-syllable words), lowercased.
/// Source: PGP word list designed by Patrick Juola and Philip Zimmermann (1995),
/// licensed under the GNU Free Documentation License.
/// Index 0–255 map to byte values 0x00–0xFF respectively.
/// MUST remain exactly 256 entries — the assert below enforces this at runtime.
const List<String> _fingerprintWords = [
  'aardvark', 'absurd',    'accrue',    'acme',
  'adrift',   'adult',     'afflict',   'ahead',
  'aimless',  'algol',     'allow',     'alone',
  'ammo',     'ancient',   'apple',     'artist',
  'assume',   'athens',    'atlas',     'aztec',
  'baboon',   'backfield', 'backward',  'basalt',
  'beaming',  'bedlamp',   'beehive',   'beeswax',
  'befriend', 'belfast',   'berserk',   'billiard',
  'bison',    'blackjack', 'blockade',  'blowtorch',
  'bluebird', 'bombast',   'bookshelf', 'brackish',
  'breadline','breakup',   'brickyard', 'briefcase',
  'burbank',  'button',    'buzzard',   'cement',
  'chairlift','chatter',   'checkup',   'chisel',
  'choking',  'chopper',   'christmas', 'clamshell',
  'classic',  'classroom', 'cleanup',   'clockwork',
  'cobra',    'commence',  'concert',   'cowbell',
  'crackdown','cranky',    'crowfoot',  'crucial',
  'crumpled', 'crusade',   'cubic',     'deadbolt',
  'deckhand', 'dogsled',   'dosage',    'dragnet',
  'drainage', 'dreadful',  'drifter',   'dropper',
  'drumbeat', 'drunken',   'dupont',    'dwelling',
  'eating',   'edict',     'egghead',   'eightball',
  'endorse',  'endow',     'enlist',    'erase',
  'escape',   'exceed',    'eyeglass',  'eyetooth',
  'facial',   'fallout',   'flagpole',  'flatfoot',
  'flytrap',  'fracture',  'fragile',   'framework',
  'freedom',  'frighten',  'gazelle',   'geiger',
  'glasgow',  'glitter',   'glucose',   'goggles',
  'goldfish', 'gremlin',   'guidance',  'hamlet',
  'highchair','hockey',    'hotdog',    'indoors',
  'indulge',  'inverse',   'involve',   'island',
  'janus',    'jawbone',   'keyboard',  'kickoff',
  'kiwi',     'klaxon',    'lockup',    'merit',
  'minnow',   'miser',     'mohawk',    'mural',
  'music',    'neptune',   'newborn',   'nightbird',
  'obtuse',   'offload',   'oilfield',  'optic',
  'orca',     'payday',    'peachy',    'pheasant',
  'physique', 'playhouse', 'pluto',     'preclude',
  'prefer',   'preshrunk', 'printer',   'profile',
  'prowler',  'pupil',     'puppy',     'python',
  'quadrant', 'quiver',    'quota',     'ragtime',
  'ratchet',  'rebirth',   'reform',    'regain',
  'reindeer', 'rematch',   'repay',     'retouch',
  'revenge',  'reward',    'rhythm',    'ringbolt',
  'robust',   'rocker',    'ruffled',   'sawdust',
  'scallion', 'scenic',    'scorecard', 'scotland',
  'seabird',  'select',    'sentence',  'shadow',
  'showgirl', 'skullcap',  'skydive',   'slingshot',
  'slothful', 'slowdown',  'snapline',  'snapshot',
  'snowcap',  'snowslide', 'solo',      'spaniel',
  'spearhead','spellbind', 'spheroid',  'spigot',
  'spindle',  'spoilage',  'spyglass',  'stagehand',
  'stagnate', 'stairway',  'standard',  'stapler',
  'steamship','stepchild', 'sterling',  'stockman',
  'stopwatch','stormy',    'sugar',     'surmount',
  'suspense', 'swelter',   'tactics',   'talon',
  'tapeworm', 'tempest',   'tiger',     'tissue',
  'tonic',    'tracker',   'transit',   'trauma',
  'treadmill','trojan',    'trouble',   'tumor',
  'tunnel',   'tycoon',    'umpire',    'uncut',
  'unearth',  'unwind',    'uproot',    'upset',
  'upshot',   'vapor',     'village',   'virus',
  'vulcan',   'waffle',    'wallet',    'watchword',
  'wayside',  'willow',    'woodlark',  'zulu',
];

/// Word fingerprint of the trusted signer set: BLAKE2s over length-prefixed,
/// byte-sorted signer pubkeys, first 8 bytes → words. Identical to Go
/// trustlog.SignerSetFingerprint.
List<String> signerSetFingerprintWords(List<Uint8List> signers) {
  assert(_fingerprintWords.length == 256);
  final sorted = [...signers]..sort(_compareBytes);
  final buf = BytesBuilder();
  final len = Uint8List(4);
  for (final s in sorted) {
    ByteData.sublistView(len).setUint32(0, s.length, Endian.big);
    buf..add(len)..add(s);
  }
  final digest = blake2s(buf.toBytes());
  final n = digest.length < 8 ? digest.length : 8;
  return [for (var i = 0; i < n; i++) _fingerprintWords[digest[i]]];
}

int _compareBytes(Uint8List a, Uint8List b) {
  final n = a.length < b.length ? a.length : b.length;
  for (var i = 0; i < n; i++) {
    if (a[i] != b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}
