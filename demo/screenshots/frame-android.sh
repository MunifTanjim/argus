#!/usr/bin/env bash
# Composite a raw Android phone screenshot into a framed Play Store image: a
# clean device mockup (black body, uniform bezel, right-side buttons, soft
# shadow) on a dark background with a caption. The device is scaled by height so
# a tall capture fits the shorter 9:16 canvas.
#
# Usage: ./frame-android.sh <raw.png> <output.png> "Caption text"
set -euo pipefail

RAW="${1:?raw png path}"
OUT="${2:?output png path}"
CAPTION="${3:?caption text}"

# Canvas size: Google Play phone screenshots default to 1080x1920 (9:16).
# Override with TARGET_W/TARGET_H. Layout scales with width.
W="${TARGET_W:-1080}"
H="${TARGET_H:-1920}"
BG_TOP="#25292b"
BG_BOTTOM="#161819"
FG="#ebdbb2"
BODY="#0a0a0a"
BUTTON="#2b2b2b"
FONT="${ARGUS_CAPTION_FONT:-/System/Library/Fonts/SFNS.ttf}"

s() { echo $(( $1 * W / 1080 )); } # scale a 1080-referenced length to the target width
CAP_H=$(s 180)       # caption text-box height (top band)
CAP_Y=$(s 70)        # caption top offset
DEV_Y=$(s 300)       # device top offset (below the caption band)
DEV_BOTTOM=$(s 70)   # margin below the device
BEZEL=$(s 18)        # uniform bezel around the screen
SR=$(s 44)           # screen corner radius
NUB=$(s 8)           # how far side buttons protrude past the body
PT=$(s 62)           # caption font point size

TMP=$(mktemp -d -t argus-frame.XXXXXX)
trap 'rm -rf "$TMP"' EXIT

# The device body/buttons are pure gray; ImageMagick would save them as
# grayscale PNGs, and compositing the color screenshot onto a grayscale base
# silently desaturates it. Forcing RGBA color-type on every intermediate keeps
# the screen's colors intact.
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
  -compose over -composite "${RGBA[@]}" "$TMP/body.png"

# Center hole-punch front camera: the Android tell (an iPhone has a Dynamic
# Island pill instead). A dark dot over the top-center of the screen.
CAM_R=$(s 15)
CAM_CX=$((BW / 2))
CAM_CY=$((BEZEL + $(s 40)))
magick "$TMP/body.png" -fill "#000000" \
  -draw "circle ${CAM_CX},${CAM_CY} ${CAM_CX},$((CAM_CY - CAM_R))" \
  "${RGBA[@]}" "$TMP/body.png"

# 3. Device canvas: body plus room for the right-side button nubs (Android
# layout: power above a volume rocker, both on the right).
DW=$((BW + NUB))
DH=$BH
BTN_W=$((NUB + 6)) # nub depth into the body so buttons attach cleanly
p_y=$((BH * 22 / 100)); p_len=$((BH * 8 / 100))   # power/side
v_y=$((BH * 33 / 100)); v_len=$((BH * 13 / 100))  # volume rocker
magick -size "${DW}x${DH}" xc:none \
  -fill "$BUTTON" \
  -draw "roundrectangle $((DW - BTN_W - 1)),${p_y},$((DW - 1)),$((p_y + p_len)),4,4" \
  -draw "roundrectangle $((DW - BTN_W - 1)),${v_y},$((DW - 1)),$((v_y + v_len)),4,4" \
  "${RGBA[@]}" "$TMP/buttons.png"
# Body sits over the button bases (left-aligned), hiding the inner button ends.
magick "$TMP/buttons.png" "$TMP/body.png" -geometry "+0+0" \
  -compose over -composite "${RGBA[@]}" "$TMP/device.png"

# 4. Soft drop shadow from the device silhouette.
magick "$TMP/device.png" \
  \( +clone -alpha extract -background black -shadow "60x34+0+24" \) \
  +swap -background none -layers merge +repage "${RGBA[@]}" "$TMP/device_sh.png"

# 5. Background + caption + device.
magick -size "${W}x${H}" \
  -define gradient:direction=north "gradient:${BG_BOTTOM}-${BG_TOP}" \
  \( -background none -fill "$FG" -gravity center \
     -font "$FONT" -pointsize "$PT" -size "$((W-150))x${CAP_H}" \
     "caption:${CAPTION}" \) \
  -gravity north -geometry "+0+${CAP_Y}" -compose over -composite \
  "$TMP/device_sh.png" -gravity north -geometry "+0+${DEV_Y}" \
  -compose over -composite \
  -alpha remove -alpha off "$OUT"

echo "wrote $OUT ($(magick identify -format '%wx%h' "$OUT"))"
