#!/usr/bin/env bash
# Composite a raw iPhone 17 Pro Max screenshot (1320x2868) into a framed
# App Store image at the same size, with a caption above the device.
#
# Usage: ./frame.sh <raw.png> <output.png> "Caption text"
set -euo pipefail

RAW="${1:?raw png path}"
OUT="${2:?output png path}"
CAPTION="${3:?caption text}"

W=1320
H=2868
BG="#1d2021"
FG="#ebdbb2"
BEZEL="#3c3836"
FONT="${ARGUS_CAPTION_FONT:-/System/Library/Fonts/SFNS.ttf}"

CAP_H=420          # caption band height
DEV_W=1080         # device width on canvas
RADIUS=72          # screen corner radius
BEZEL_PAD=14       # bezel thickness

# Scale the screenshot to the device width, keep aspect.
TMPDIR_RUN=$(mktemp -d -t argus-frame.XXXXXX)
trap 'rm -rf "$TMPDIR_RUN"' EXIT
SCREEN="$TMPDIR_RUN/screen.png"
magick "$RAW" -resize "${DEV_W}x" \
  \( +clone -alpha extract -draw "fill black polygon 0,0 0,$RADIUS $RADIUS,0 fill white circle $RADIUS,$RADIUS $RADIUS,0" \
     \( +clone -flip \) -compose Multiply -composite \
     \( +clone -flop \) -compose Multiply -composite \) \
  -alpha off -compose CopyOpacity -composite "$SCREEN"

# Add a bezel border around the rounded screen.
DEVICE="$TMPDIR_RUN/device.png"
magick "$SCREEN" -bordercolor none -border "$BEZEL_PAD" \
  \( +clone -alpha extract -morphology Dilate "Disk:${BEZEL_PAD}" -fill "$BEZEL" -colorize 100 \) \
  +swap -compose over -composite "$DEVICE"

# Build canvas, add caption, center the device below it.
magick -size "${W}x${H}" "canvas:${BG}" \
  \( -background none -fill "$FG" -gravity center \
     -font "$FONT" -pointsize 84 -size "$((W-160))x${CAP_H}" \
     "caption:${CAPTION}" \) \
  -gravity north -geometry +0+120 -compose over -composite \
  "$DEVICE" -gravity north -geometry "+0+${CAP_H}" -compose over -composite \
  -alpha remove -alpha off "$OUT"

echo "wrote $OUT ($(magick identify -format '%wx%h' "$OUT"))"
