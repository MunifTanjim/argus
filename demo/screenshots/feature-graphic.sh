#!/usr/bin/env bash
# Compose the Google Play "Feature graphic": a 1024x500 promo banner with the
# argus icon and wordmark (a centered lockup) on the left and a tilted phone
# screenshot on the right, on the framed-screenshot dark gradient. The phone is
# built at 2x and downscaled so the tilt stays sharp.
#
# Usage: ./feature-graphic.sh <icon.png> <phone-raw.png> <output.png>
set -euo pipefail

ICON="${1:?icon png path}"
PHONE="${2:?phone raw screenshot path}"
OUT="${3:?output png path}"

W=1024
H=500
BG_TOP="#25292b"
BG_BOTTOM="#161819"
TITLE_COLOR="#FFCD00"
TAG_COLOR="#ffffff"
BODY="#0a0a0a"
FONT="${ARGUS_CAPTION_FONT:-/System/Library/Fonts/SFNS.ttf}"
TITLE="${ARGUS_TITLE:-Argus}"
TAGLINE="${ARGUS_TAGLINE:-Watch & Control your AI Agents}"
ROT="${ARGUS_PHONE_ROT:--5}"   # phone tilt in degrees
MARGIN=72
PHONE_TOP=12                   # top margin of the phone (bottom bleeds off)

TMP=$(mktemp -d -t argus-feat.XXXXXX)
trap 'rm -rf "$TMP"' EXIT
RGBA=(-define png:color-type=6)

# 1. Phone, built at 2x for a sharp tilt: scale the raw, round corners, wrap a
#    thin dark body, add a soft shadow, rotate, then downscale to 1x.
SS=2
PH=$(( 760 * SS ))
magick "$PHONE" -filter Lanczos -resize "x${PH}" "$TMP/ph.png"
read -r PW PHh < <(magick identify -format '%w %h\n' "$TMP/ph.png")
SR=$(( 28 * SS ))
magick "$TMP/ph.png" \
  \( -size "${PW}x${PHh}" xc:none -fill white -draw "roundrectangle 0,0,$((PW-1)),$((PHh-1)),$SR,$SR" \) \
  -alpha set -compose DstIn -composite "$TMP/ph_r.png"
BEZEL=$(( 10 * SS ))
BW=$((PW + 2 * BEZEL)); BH=$((PHh + 2 * BEZEL)); BR=$((SR + BEZEL))
magick -size "${BW}x${BH}" xc:none -fill "$BODY" \
  -draw "roundrectangle 0,0,$((BW-1)),$((BH-1)),$BR,$BR" "${RGBA[@]}" "$TMP/body.png"
magick "$TMP/body.png" "$TMP/ph_r.png" -geometry "+${BEZEL}+${BEZEL}" \
  -compose over -composite "${RGBA[@]}" "$TMP/device.png"
magick "$TMP/device.png" -background none -filter Lanczos -rotate "$ROT" +repage \
  -resize "$(( 100 / SS ))%" "${RGBA[@]}" "$TMP/device_t.png"
read -r DTW DTH < <(magick identify -format '%w %h\n' "$TMP/device_t.png")

# 2. Left lockup: icon and wordmark on one row, vertically centered to each
#    other; tagline aligned under the icon's left edge. Canvases are sized with
#    a few extra px so trimmed glyphs are never clipped.
IH=140
magick "$ICON" -resize "${IH}x${IH}" "$TMP/icon.png"
magick -background none -fill "$TITLE_COLOR" -font "$FONT" -pointsize 118 label:"$TITLE" -trim +repage "$TMP/word.png"
read -r WW WH < <(magick identify -format '%w %h\n' "$TMP/word.png")
magick -background none -fill "$TAG_COLOR" -font "$FONT" -pointsize 35 label:"$TAGLINE" -trim +repage "$TMP/tag.png"
read -r TGW TGH < <(magick identify -format '%w %h\n' "$TMP/tag.png")

GAP=28
PAD=6
ROW_H=$(( IH > WH ? IH : WH ))
ROW_W=$(( IH + GAP + WW + PAD ))
magick -size "${ROW_W}x${ROW_H}" xc:none \
  "$TMP/icon.png" -geometry "+0+$(( (ROW_H - IH) / 2 ))" -compose over -composite \
  "$TMP/word.png" -geometry "+$(( IH + GAP ))+$(( (ROW_H - WH) / 2 ))" -compose over -composite \
  "${RGBA[@]}" "$TMP/row.png"

BLK_GAP=30
BLK_W=$(( ROW_W > TGW + PAD ? ROW_W : TGW + PAD ))
BLK_H=$(( ROW_H + BLK_GAP + TGH ))
magick -size "${BLK_W}x${BLK_H}" xc:none \
  "$TMP/row.png" -geometry "+0+0" -compose over -composite \
  "$TMP/tag.png" -geometry "+4+$(( ROW_H + BLK_GAP ))" -compose over -composite \
  "${RGBA[@]}" "$TMP/block.png"

# 3. Background + phone (right, top visible / bottom bleeding) + left lockup.
PHONE_X=$(( W - DTW + 10 ))
PHONE_Y=$PHONE_TOP
BLK_Y=$(( (H - BLK_H) / 2 ))
magick -size "${W}x${H}" -define gradient:direction=north "gradient:${BG_BOTTOM}-${BG_TOP}" \
  "$TMP/device_t.png" -geometry "+${PHONE_X}+${PHONE_Y}" -compose over -composite \
  "$TMP/block.png" -geometry "+${MARGIN}+${BLK_Y}" -compose over -composite \
  -alpha remove -alpha off "$OUT"

echo "wrote $OUT ($(magick identify -format '%wx%h' "$OUT"))"
