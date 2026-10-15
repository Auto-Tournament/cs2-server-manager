# web_assets

Compressed copies of `map_thumbnails/` and `skin_images/` in the sizes the Auto Tournament
platform shows. Its CS2 module bundles this folder, so map and skin pictures load from the
platform itself. `scripts/make-web-assets.sh` writes it; the Game assets workflow runs that after
every extract. Do not edit by hand.

| Folder | Size | Use |
| --- | --- | --- |
| `maps/1280/` | 1280 px wide, WebP q78 | map headers, veto |
| `maps/480/` | 480 px wide, WebP q72 | map cards, lists |
| `maps/icons/` | SVG, as extracted | map icons |
| `skins/256/` | 256 px wide, WebP q75 | skin cards, inventories (no wear looks) |
