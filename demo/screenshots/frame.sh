#!/usr/bin/env bash
# Composite a raw iPhone 17 Pro Max screenshot (1320x2868) into a framed App
# Store image at the same size: a clean device mockup (black body, uniform
# bezel, side buttons, soft shadow) on a dark background with a caption.
#
# Usage: ./frame.sh <raw.png> <output.png> "Caption text"
set -euo pipefail

RAW="${1:?raw png path}"
OUT="${2:?output png path}"
CAPTION="${3:?caption text}"

# Canvas size: App Store Connect accepts 6.9" (1320x2868, default) or, for the
# 6.5" slot, 1284x2778. Override with TARGET_W/TARGET_H. Layout scales with width.
W="${TARGET_W:-1320}"
H="${TARGET_H:-2868}"
BG_TOP="#25292b"
BG_BOTTOM="#161819"
FG="#ebdbb2"
BODY="#0a0a0a"
BUTTON="#2b2b2b"
FONT="${ARGUS_CAPTION_FONT:-/System/Library/Fonts/SFNS.ttf}"

s() { echo $(( $1 * W / 1320 )); } # scale a 1320-referenced length to the target width
CAP_H=$(s 224)   # caption text-box height (top band)
CAP_Y=$(s 92)    # caption top offset
DEV_Y=$(s 336)   # device top offset (below the caption band)
DEV_W=$(s 1060)  # on-canvas screen width (the raw is scaled to this)
BEZEL=$(s 22)    # uniform bezel around the screen
SR=$(s 94)       # screen corner radius
NUB=$(s 9)       # how far side buttons protrude past the body
PT=$(s 80)       # caption font point size

TMP=$(mktemp -d -t argus-frame.XXXXXX)
trap 'rm -rf "$TMP"' EXIT

# The device body/buttons are pure gray; ImageMagick would save them as
# grayscale PNGs, and compositing the color screenshot onto a grayscale base
# silently desaturates it. Forcing RGBA color-type on every intermediate keeps
# the screen's colors intact.
RGBA=(-define png:color-type=6)

# 1. Scale the screenshot and round its corners.
magick "$RAW" -resize "${DEV_W}x" "$TMP/screen.png"
read -r SW SH < <(magick identify -format '%w %h\n' "$TMP/screen.png")
magick "$TMP/screen.png" \
  \( -size "${SW}x${SH}" xc:none -fill white -draw "roundrectangle 0,0,$((SW-1)),$((SH-1)),$SR,$SR" \) \
  -alpha set -compose DstIn -composite "$TMP/screen_r.png"

# 2. Black body: a rounded rect one bezel larger than the screen on every side.
BW=$((SW + 2 * BEZEL))
BH=$((SH + 2 * BEZEL))
BR=$((SR + BEZEL))
magick -size "${BW}x${BH}" xc:none -fill "$BODY" \
  -draw "roundrectangle 0,0,$((BW-1)),$((BH-1)),$BR,$BR" "${RGBA[@]}" "$TMP/body.png"
# Inset the rounded screen into the body.
magick "$TMP/body.png" "$TMP/screen_r.png" -geometry "+${BEZEL}+${BEZEL}" \
  -compose over -composite "${RGBA[@]}" "$TMP/body.png"

# 3. Device canvas: body plus room for the side-button nubs.
DW=$((BW + 2 * NUB))
DH=$BH
BTN_W=$((NUB + 6)) # nub depth into the body so buttons attach cleanly
# Button spans as fractions of body height (top-anchored).
r_y=$((BH * 23 / 100)); r_len=$((BH * 11 / 100))       # right: power/side
la_y=$((BH * 20 / 100)); la_len=$((BH * 5 / 100))      # left: action button
lu_y=$((BH * 29 / 100)); lu_len=$((BH * 8 / 100))      # left: volume up
ld_y=$((BH * 40 / 100)); ld_len=$((BH * 8 / 100))      # left: volume down
magick -size "${DW}x${DH}" xc:none \
  -fill "$BUTTON" \
  -draw "roundrectangle 0,${la_y},$((BTN_W)),$((la_y + la_len)),4,4" \
  -draw "roundrectangle 0,${lu_y},$((BTN_W)),$((lu_y + lu_len)),4,4" \
  -draw "roundrectangle 0,${ld_y},$((BTN_W)),$((ld_y + ld_len)),4,4" \
  -draw "roundrectangle $((DW - BTN_W - 1)),${r_y},$((DW - 1)),$((r_y + r_len)),4,4" \
  "${RGBA[@]}" "$TMP/buttons.png"
# Body sits over the button bases (nub offset), hiding the inner button ends.
magick "$TMP/buttons.png" "$TMP/body.png" -geometry "+${NUB}+0" \
  -compose over -composite "${RGBA[@]}" "$TMP/device.png"

# 4. Soft drop shadow from the device silhouette.
magick "$TMP/device.png" \
  \( +clone -alpha extract -background black -shadow "60x34+0+24" \) \
  +swap -background none -layers merge +repage "${RGBA[@]}" "$TMP/device_sh.png"

# 5. Background + caption + device.
magick -size "${W}x${H}" \
  -define gradient:direction=north "gradient:${BG_BOTTOM}-${BG_TOP}" \
  \( -background none -fill "$FG" -gravity center \
     -font "$FONT" -pointsize "$PT" -size "$((W-180))x${CAP_H}" \
     "caption:${CAPTION}" \) \
  -gravity north -geometry "+0+${CAP_Y}" -compose over -composite \
  "$TMP/device_sh.png" -gravity north -geometry "+0+${DEV_Y}" \
  -compose over -composite \
  -alpha remove -alpha off "$OUT"

echo "wrote $OUT ($(magick identify -format '%wx%h' "$OUT"))"
