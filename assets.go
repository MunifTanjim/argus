// Package argus embeds shared assets reused across the Go binaries (e.g. the app
// icon for desktop notifications), so they aren't duplicated into subpackages.
// Embed paths can only descend from the embedding file's directory, hence this
// lives at the module root where it can reach app/.
package argus

import _ "embed"

// IconPNG is the argus app icon (the 192×192 Android launcher icon — small and
// color), used to brand desktop notifications.
//
//go:embed app/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png
var IconPNG []byte
