#!/usr/bin/env bash
# Small copies of the skin pictures for lists and cards: skin_images/<name>.webp (512 px) ->
# skin_thumbs/<name>.webp (256 px wide, WebP q75, about a quarter of the bytes). The Auto
# Tournament CS2 module ships these, so inventories show skins with no internet; the full-size
# pictures are fetched and kept by the platform. Wear variants (_medium, _heavy) are not thumbed.
#
#   scripts/make-skin-thumbs.sh           # new or changed pictures only
#   scripts/make-skin-thumbs.sh --all     # every picture again
#
# Needs cwebp and dwebp (libwebp).
set -euo pipefail
cd "$(dirname "$0")/.."
command -v cwebp >/dev/null && command -v dwebp >/dev/null || { echo "needs cwebp and dwebp (libwebp)" >&2; exit 1; }
all=0
[[ "${1:-}" == "--all" ]] && all=1
mkdir -p skin_thumbs
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
made=0
for src in skin_images/*.webp; do
  name="$(basename "$src")"
  case "$name" in *_medium.webp|*_heavy.webp) continue ;; esac
  dst="skin_thumbs/$name"
  if [[ $all -eq 0 && -f "$dst" && "$dst" -nt "$src" ]]; then continue; fi
  dwebp -quiet "$src" -o "$tmp/x.png"
  cwebp -quiet -q 75 -alpha_q 80 -resize 256 0 "$tmp/x.png" -o "$dst"
  made=$((made + 1))
done
echo "skin_thumbs: $made made, $(ls skin_thumbs | wc -l | tr -d ' ') total, $(du -sh skin_thumbs | cut -f1)"
