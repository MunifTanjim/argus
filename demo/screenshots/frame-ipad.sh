#!/usr/bin/env bash
# Composite a raw iPad Pro 13" screenshot (2064x2752) into a framed App Store
# image: a clean tablet mockup (black body, thin uniform bezel, soft shadow) on
# a dark background with a caption. The device is scaled by height so the capture
# fits below the caption band.
#
# Usage: ./frame-ipad.sh <raw.png> <output.png> "Caption text"
set -euo pipefail

RAW="${1:?raw png path}"
OUT="${2:?output png path}"
CAPTION="${3:?caption text}"

# Canvas size: App Store Connect accepts the 13" iPad slot at 2064x2752
# (default) or 2048x2732. Override with TARGET_W/TARGET_H. Layout scales width.
W="${TARGET_W:-2064}"
H="${TARGET_H:-2752}"
BG_TOP="#25292b"
BG_BOTTOM="#161819"
FG="#ebdbb2"
BODY="#0a0a0a"
FONT="${ARGUS_CAPTION_FONT:-/System/Library/Fonts/SFNS.ttf}"

s() { echo $(( $1 * W / 2064 )); } # scale a 2064-referenced length to the target width
CAP_H=$(s 300)       # caption text-box height (top band)
CAP_Y=$(s 130)       # caption top offset
DEV_Y=$(s 500)       # device top offset (below the caption band)
DEV_BOTTOM=$(s 120)  # margin below the device
BEZEL=$(s 28)        # thin uniform bezel around the screen
SR=$(s 60)           # screen corner radius
PT=$(s 108)          # caption font point size

TMP=$(mktemp -d -t argus-frame.XXXXXX)
trap 'rm -rf "$TMP"' EXIT

# The device body is pure gray; ImageMagick would save it as a grayscale PNG,
# and compositing the color screenshot onto a grayscale base silently
# desaturates it. Forcing RGBA color-type on every intermediate keeps the
# screen's colors intact.
RGBA=(-define png:color-type=6)

# 1. Scale the screenshot to fit the available device height, then round corners.
AVAIL_H=$(( H - DEV_Y - DEV_BOTTOM ))
SH_T=$(( AVAIL_H - 2 * BEZEL ))
magick "$RAW" -resize "x${SH_T}" "$TMP/screen.png"
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
  -compose over -composite "${RGBA[@]}" "$TMP/device.png"

# 3. Soft drop shadow from the device silhouette.
magick "$TMP/device.png" \
  \( +clone -alpha extract -background black -shadow "60x40+0+28" \) \
  +swap -background none -layers merge +repage "${RGBA[@]}" "$TMP/device_sh.png"

# 4. Background + caption + device.
magick -size "${W}x${H}" \
  -define gradient:direction=north "gradient:${BG_BOTTOM}-${BG_TOP}" \
  \( -background none -fill "$FG" -gravity center \
     -font "$FONT" -pointsize "$PT" -size "$((W-200))x${CAP_H}" \
     "caption:${CAPTION}" \) \
  -gravity north -geometry "+0+${CAP_Y}" -compose over -composite \
  "$TMP/device_sh.png" -gravity north -geometry "+0+${DEV_Y}" \
  -compose over -composite \
  -alpha remove -alpha off "$OUT"

echo "wrote $OUT ($(magick identify -format '%wx%h' "$OUT"))"
