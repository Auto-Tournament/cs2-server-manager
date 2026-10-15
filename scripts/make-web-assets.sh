#!/usr/bin/env bash
# Compressed copies of the extracted pictures, in the sizes the Auto Tournament platform shows,
# so its CS2 module can bundle them and serve them itself (no CDN on the page):
#
#   web_assets/maps/1280/<map>.webp     map screenshots for headers and the veto (WebP q78)
#   web_assets/maps/480/<map>.webp      map cards and lists (WebP q72)
#   web_assets/maps/icons/<map>.svg     map icons, as extracted
#   web_assets/skins/256/<skin>.webp    skin cards and inventories (q75)
#
# Sources: map_thumbnails/<map>.webp (1920x1080; *_thumb.webp and the PNGs are skipped) and
# skin_images/<skin>.webp (512 px). The full skin pictures (skin_images/, already WebP: a
# re-encode saves about 15 %) are fetched and kept by the platform itself. The Game assets
# workflow runs this after every extract.
#
#   scripts/make-web-assets.sh           # new or changed pictures only
#   scripts/make-web-assets.sh --all     # every picture again
#
# Needs cwebp and dwebp (libwebp).
set -euo pipefail
cd "$(dirname "$0")/.."
command -v cwebp >/dev/null && command -v dwebp >/dev/null || { echo "needs cwebp and dwebp (libwebp)" >&2; exit 1; }
all=0
[[ "${1:-}" == "--all" ]] && all=1
out=web_assets
mkdir -p "$out/maps/1280" "$out/maps/480" "$out/maps/icons" "$out/skins/256"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# <src> <dst> <width> <quality>: re-encode when dst is missing or older than src.
encode() {
  local src="$1" dst="$2" width="$3" q="$4"
  if [[ $all -eq 0 && -f "$dst" && "$dst" -nt "$src" ]]; then return 0; fi
  local png="$tmp/$$.$RANDOM.png"
  dwebp -quiet "$src" -o "$png"
  cwebp -quiet -q "$q" -alpha_q 80 -m 6 -resize "$width" 0 "$png" -o "$dst"
  rm -f "$png"
}
export -f encode
export all tmp

jobs=$( (nproc || sysctl -n hw.ncpu) 2>/dev/null | head -1)
{
  for src in map_thumbnails/*.webp; do
    name="$(basename "$src")"
    case "$name" in *_thumb.webp) continue ;; esac
    echo "$src $out/maps/1280/$name 1280 78"
    echo "$src $out/maps/480/$name 480 72"
  done
  for src in skin_images/*.webp; do
    name="$(basename "$src")"
    case "$name" in *_medium.webp|*_heavy.webp) continue ;; esac
    echo "$src $out/skins/256/$name 256 75"
  done
} | xargs -P "${jobs:-4}" -n 4 bash -c 'encode "$@"' _

cp -p map_thumbnails/*.svg "$out/maps/icons/" 2>/dev/null || true
# Pictures whose source is gone.
for f in "$out"/maps/1280/*.webp "$out"/maps/480/*.webp; do
  [[ -f "map_thumbnails/$(basename "$f")" ]] || rm -f "$f"
done
for f in "$out"/skins/256/*.webp; do
  [[ -f "skin_images/$(basename "$f")" ]] || rm -f "$f"
done
for d in maps/1280 maps/480 maps/icons skins/256; do
  echo "$out/$d: $(ls "$out/$d" | wc -l | tr -d ' ') files, $(du -sh "$out/$d" | cut -f1)"
done
