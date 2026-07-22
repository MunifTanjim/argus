package trustlog

import (
	"bytes"
	"encoding/binary"
	"sort"

	"golang.org/x/crypto/blake2s"
)

// fingerprintWordList maps byte values 0x00–0xFF to words.
// Copied verbatim from app/lib/e2e/trust_fingerprint.dart (_fingerprintWords).
// PGP biometric word list (even-index, two-syllable words), lowercased.
var fingerprintWordList = [256]string{
	"aardvark", "absurd", "accrue", "acme",
	"adrift", "adult", "afflict", "ahead",
	"aimless", "algol", "allow", "alone",
	"ammo", "ancient", "apple", "artist",
	"assume", "athens", "atlas", "aztec",
	"baboon", "backfield", "backward", "basalt",
	"beaming", "bedlamp", "beehive", "beeswax",
	"befriend", "belfast", "berserk", "billiard",
	"bison", "blackjack", "blockade", "blowtorch",
	"bluebird", "bombast", "bookshelf", "brackish",
	"breadline", "breakup", "brickyard", "briefcase",
	"burbank", "button", "buzzard", "cement",
	"chairlift", "chatter", "checkup", "chisel",
	"choking", "chopper", "christmas", "clamshell",
	"classic", "classroom", "cleanup", "clockwork",
	"cobra", "commence", "concert", "cowbell",
	"crackdown", "cranky", "crowfoot", "crucial",
	"crumpled", "crusade", "cubic", "deadbolt",
	"deckhand", "dogsled", "dosage", "dragnet",
	"drainage", "dreadful", "drifter", "dropper",
	"drumbeat", "drunken", "dupont", "dwelling",
	"eating", "edict", "egghead", "eightball",
	"endorse", "endow", "enlist", "erase",
	"escape", "exceed", "eyeglass", "eyetooth",
	"facial", "fallout", "flagpole", "flatfoot",
	"flytrap", "fracture", "fragile", "framework",
	"freedom", "frighten", "gazelle", "geiger",
	"glasgow", "glitter", "glucose", "goggles",
	"goldfish", "gremlin", "guidance", "hamlet",
	"highchair", "hockey", "hotdog", "indoors",
	"indulge", "inverse", "involve", "island",
	"janus", "jawbone", "keyboard", "kickoff",
	"kiwi", "klaxon", "lockup", "merit",
	"minnow", "miser", "mohawk", "mural",
	"music", "neptune", "newborn", "nightbird",
	"obtuse", "offload", "oilfield", "optic",
	"orca", "payday", "peachy", "pheasant",
	"physique", "playhouse", "pluto", "preclude",
	"prefer", "preshrunk", "printer", "profile",
	"prowler", "pupil", "puppy", "python",
	"quadrant", "quiver", "quota", "ragtime",
	"ratchet", "rebirth", "reform", "regain",
	"reindeer", "rematch", "repay", "retouch",
	"revenge", "reward", "rhythm", "ringbolt",
	"robust", "rocker", "ruffled", "sawdust",
	"scallion", "scenic", "scorecard", "scotland",
	"seabird", "select", "sentence", "shadow",
	"showgirl", "skullcap", "skydive", "slingshot",
	"slothful", "slowdown", "snapline", "snapshot",
	"snowcap", "snowslide", "solo", "spaniel",
	"spearhead", "spellbind", "spheroid", "spigot",
	"spindle", "spoilage", "spyglass", "stagehand",
	"stagnate", "stairway", "standard", "stapler",
	"steamship", "stepchild", "sterling", "stockman",
	"stopwatch", "stormy", "sugar", "surmount",
	"suspense", "swelter", "tactics", "talon",
	"tapeworm", "tempest", "tiger", "tissue",
	"tonic", "tracker", "transit", "trauma",
	"treadmill", "trojan", "trouble", "tumor",
	"tunnel", "tycoon", "umpire", "uncut",
	"unearth", "unwind", "uproot", "upset",
	"upshot", "vapor", "village", "virus",
	"vulcan", "waffle", "wallet", "watchword",
	"wayside", "willow", "woodlark", "zulu",
}

// fingerprintWords maps the first up-to-8 bytes of digest to words.
func fingerprintWords(digest []byte) []string {
	n := len(digest)
	if n > 8 {
		n = 8
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fingerprintWordList[digest[i]]
	}
	return out
}

// SignerSetFingerprint is the human-verifiable word fingerprint of the trusted
// signer set: BLAKE2s over length-prefixed, byte-sorted signer pubkeys, first 8
// bytes → words. Deterministic and identical to the Flutter client.
func SignerSetFingerprint(signers [][]byte) []string {
	sorted := make([][]byte, len(signers))
	copy(sorted, signers)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
	var buf bytes.Buffer
	var n [4]byte
	for _, s := range sorted {
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		buf.Write(n[:])
		buf.Write(s)
	}
	sum := blake2s.Sum256(buf.Bytes())
	return fingerprintWords(sum[:])
}
