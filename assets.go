// Package argus embeds shared assets. It lives at the module root because embed
// paths can only descend from the embedding file's directory (needs app/).
package argus

import _ "embed"

// IconPNG is the argus app icon, used to brand desktop notifications.
//
//go:embed app/android/app/src/main/res/mipmap-xxxhdpi/ic_launcher.png
var IconPNG []byte
