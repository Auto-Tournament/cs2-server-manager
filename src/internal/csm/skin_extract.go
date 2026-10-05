package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SkinsManifestFile is written next to the images in skin_images/: every
// weapon skin the game ships, with its name, rarity and image file.
const SkinsManifestFile = "skins.json"

// SkinDataOptions configures ExtractSkinData.
type SkinDataOptions struct {
	// Progress, when set, receives every log line as it is written.
	Progress io.Writer
	// Width is the largest width of a written image in px (default 512).
	Width int
}

// ExtractSkinData pulls the weapon skin previews out of pak01_dir.vpk and
// writes them to skin_images/ under the working directory, one WEBP per
// weapon and paint kit (the game's "light" wear preview), plus skins.json:
// the weapon, paint kit id, English name and rarity of each, read from
// scripts/items/items_game.txt and resource/csgo_english.txt.
//
// Unchanged images are left alone, so a rerun after a CS2 update only
// rewrites what Valve changed.
func ExtractSkinData(ctx context.Context, opts SkinDataOptions) (string, error) {
	start := time.Now()
	var buf bytes.Buffer
	out := &multiLogWriter{writers: []io.Writer{&buf}}
	if opts.Progress != nil {
		out.writers = append(out.writers, opts.Progress)
	}
	log := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format+"\n", args...)
	}

	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	cs2User := getenvDefault("CS2_USER", DefaultCS2User)
	vpkFile := filepath.Join("/home", cs2User, "master-install", "game", "csgo", "pak01_dir.vpk")
	outDir := filepath.Join(root, "skin_images")
	width := opts.Width
	if width <= 0 {
		width = 512
	}

	if fi, err := os.Stat(vpkFile); err != nil || fi.IsDir() {
		err := fmt.Errorf("target VPK not found at %s (CS2_USER=%s)", vpkFile, cs2User)
		log("[!] %v", err)
		return buf.String(), err
	}

	log("════════════════════════════════════════════════════════")
	log("  Extract weapon skin images + skins.json")
	log("════════════════════════════════════════════════════════")
	log("VPK:     %s", vpkFile)
	log("Output:  %s", outDir)
	log("")

	log("[1/2] Checking Python (vpk + Pillow) ...")
	py, err := resolveThumbnailPython(ctx, out, defaultPythonEnv())
	if err != nil {
		log("[!] Skin extraction needs Python with the vpk and Pillow modules: %v", err)
		return buf.String(), err
	}
	log("      Python: %s", py)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return buf.String(), err
	}

	log("[2/2] Reading items_game.txt and converting the previews ...")
	cmd := exec.CommandContext(ctx, py, "-u", "-c", extractSkinsScript, vpkFile, outDir, fmt.Sprint(width), SkinsManifestFile)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		log("[!] Skin extraction failed: %v", err)
		return buf.String(), fmt.Errorf("python skin extraction failed: %w", err)
	}
	log("Done in %s. Output: %s", time.Since(start).Round(time.Second), outDir)
	return buf.String(), nil
}

// extractSkinsScript does the whole job in one Python process: 2000+ images
// would be 2000+ interpreter starts with one process per file.
//
// The previews are not PNGs inside a resource like the map screenshots: they
// are BGRA8888 textures, each mip level LZ4-compressed (the
// COMPRESSED_MIP_SIZE extra data lists the sizes, largest level first; the
// data runs smallest level first), laid out as ValveResourceFormat reads
// them. The lz4 module is used when the Python has it, else a pure-Python
// LZ4 block decoder (a few hundredths of a second per image).
const extractSkinsScript = `
import io
import json
import os
import re
import struct
import sys

import vpk
from PIL import Image

vpk_file, out_dir, width, manifest_name = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
IMAGES = "panorama/images/econ/default_generated/"
SUFFIX = "_light_png.vtex_c"

pak = vpk.open(vpk_file)


def kv_parse(text):
    """Valve KeyValues as nested lists of (key, value) pairs; keys repeat."""
    stack, key = [[]], None
    for quoted, brace, comment in re.findall(r'"((?:[^"\\]|\\.)*)"|([{}])|(//[^\n]*)', text):
        if comment:
            continue
        if brace == "{":
            node = []
            stack[-1].append((key, node))
            stack.append(node)
            key = None
        elif brace == "}":
            stack.pop()
            key = None
        elif key is None:
            key = quoted
        else:
            stack[-1].append((key, quoted))
            key = None
    return stack[0]


def get(node, k):
    for kk, v in node:
        if kk and kk.lower() == k:
            return v
    return None


def blocks(node, k):
    return [v for kk, v in node if kk and kk.lower() == k and isinstance(v, list)]


def read_text(path):
    raw = pak.get_file(path).read()
    for enc in ("utf-8-sig", "utf-16"):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            pass
    return raw.decode("utf-8", "replace")


root = get(kv_parse(read_text("scripts/items/items_game.txt")), "items_game")
english = {}
for k, v in re.findall(r'^\s*"([^"]+)"\s+"((?:[^"\\]|\\.)*)"', read_text("resource/csgo_english.txt"), re.M):
    english.setdefault(k.lower(), v)


def tr(token):
    return english.get(token.lstrip("#").lower(), token) if token else None


paint_kits, rarity, weapons = {}, {}, {}
for block in blocks(root, "paint_kits"):
    for pid, pk in block:
        if isinstance(pk, list) and get(pk, "name"):
            paint_kits[get(pk, "name").lower()] = (int(pid), get(pk, "name"), get(pk, "description_tag"))
for block in blocks(root, "paint_kits_rarity"):
    for name, r in block:
        rarity[name.lower()] = r
prefabs = {}
for block in blocks(root, "prefabs"):
    for name, pf in block:
        if isinstance(pf, list):
            prefabs[name.lower()] = pf


def item_name(node, depth=0):
    """An item's item_name, or its prefab's (items inherit through prefab chains)."""
    own = get(node, "item_name")
    if own or depth > 8:
        return own
    for parent in (get(node, "prefab") or "").split():
        if parent.lower() in prefabs:
            found = item_name(prefabs[parent.lower()], depth + 1)
            if found:
                return found
    return None


for block in blocks(root, "items"):
    for iid, it in block:
        if isinstance(it, list) and get(it, "name"):
            weapons[get(it, "name").lower()] = (get(it, "name"), tr(item_name(it)))

classes = sorted({w[0] for w in weapons.values()}, key=len, reverse=True)
sources = sorted(p.replace("\\", "/") for p in pak if p.replace("\\", "/").startswith(IMAGES) and p.endswith(SUFFIX))
print(f"      {len(paint_kits)} paint kits, {len(weapons)} items, {len(sources)} previews", flush=True)


try:
    from lz4.block import decompress as _lz4

    def lz4_block(src, size):
        return _lz4(src, uncompressed_size=size)
except ImportError:
    def lz4_block(src, size):
        dst = bytearray()
        i, n = 0, len(src)
        while i < n:
            token = src[i]
            i += 1
            lit = token >> 4
            if lit == 15:
                while True:
                    b = src[i]
                    i += 1
                    lit += b
                    if b != 255:
                        break
            dst += src[i:i + lit]
            i += lit
            if i >= n:
                break
            off = src[i] | (src[i + 1] << 8)
            i += 2
            ml = token & 15
            if ml == 15:
                while True:
                    b = src[i]
                    i += 1
                    ml += b
                    if b != 255:
                        break
            ml += 4
            start = len(dst) - off
            if off >= ml:
                dst += dst[start:start + ml]
            else:
                # Overlapping copy: the last off bytes, repeated.
                dst += (dst[start:] * (ml // off + 1))[:ml]
        return bytes(dst)

# VTexFormat values (ValveResourceFormat): how many bytes a pixel takes.
BGRA8888, RGBA8888 = 28, 4


def decode(d):
    start = d.find(b"\x89PNG")
    if start != -1:
        end = d.find(b"IEND", start)
        if end != -1:
            return Image.open(io.BytesIO(d[start:end + 8]))
    header_size, _, _, block_off, block_count = struct.unpack_from("<IHHII", d, 0)
    data = None
    for i in range(block_count):
        o = 8 + block_off + i * 12
        if d[o:o + 4] == b"DATA":
            rel, _ = struct.unpack_from("<II", d, o + 4)
            data = o + 4 + rel
    if data is None:
        raise ValueError("no DATA block")
    o = data + 4 + 16
    w, h, _depth = struct.unpack_from("<HHH", d, o)
    fmt, mips = struct.unpack_from("<BB", d, o + 6)
    eoff, ecnt = struct.unpack_from("<II", d, o + 12)
    if fmt not in (BGRA8888, RGBA8888):
        raise ValueError(f"texture format {fmt} not supported")
    sizes = None
    base = o + 12 + eoff
    for i in range(ecnt):
        t, rel, _sz = struct.unpack_from("<III", d, base + i * 12)
        if t == 4:  # COMPRESSED_MIP_SIZE: unk, unk, count, then one size per mip
            p = base + i * 12 + 4 + rel
            count = struct.unpack_from("<I", d, p + 8)[0]
            sizes = list(struct.unpack_from(f"<{count}i", d, p + 12))
    # Mips are stored smallest first; the full-size image is the last one.
    pos = header_size
    raw = None
    for level in range(mips - 1, -1, -1):
        mw, mh = max(1, w >> level), max(1, h >> level)
        want = mw * mh * 4
        if sizes:
            chunk = d[pos:pos + sizes[level]]
            pos += len(chunk)
            raw = chunk if len(chunk) == want else lz4_block(chunk, want)
        else:
            raw = d[pos:pos + want]
            pos += want
    mode = "BGRA" if fmt == BGRA8888 else "RGBA"
    return Image.frombytes("RGBA", (w, h), raw, "raw", mode)



skins, unmatched, updated, unchanged, failed = [], 0, 0, 0, 0
total = len(sources)
for i, path in enumerate(sources, 1):
    stem = path.rsplit("/", 1)[1][: -len(SUFFIX)]
    cls = next((c for c in classes if stem.startswith(c + "_") and stem[len(c) + 1:].lower() in paint_kits), None)
    if cls is None:
        unmatched += 1
        continue
    pid, pk_name, tag = paint_kits[stem[len(cls) + 1:].lower()]
    weapon, weapon_name = weapons[cls.lower()]
    image = stem + ".webp"
    try:
        img = decode(pak.get_file(path).read()).convert("RGBA")
        if img.width > width:
            img = img.resize((width, round(img.height * width / img.width)), Image.LANCZOS)
        data = io.BytesIO()
        img.save(data, "WEBP", quality=85, method=4)
        data = data.getvalue()
        dst = os.path.join(out_dir, image)
        old = open(dst, "rb").read() if os.path.exists(dst) else None
        if old == data:
            unchanged += 1
        else:
            with open(dst, "wb") as f:
                f.write(data)
            updated += 1
    except Exception as e:
        print(f"      [!] {stem}: {e}", flush=True)
        failed += 1
        continue
    skins.append({
        "weapon": weapon,
        "weaponName": weapon_name,
        "paintKit": pid,
        "paintKitName": pk_name,
        "name": tr(tag),
        "rarity": rarity.get(pk_name.lower()),
        "image": image,
    })
    if i % 100 == 0 or i == total:
        print(f"      {i}/{total} · {i * 100 // total}%", flush=True)

skins.sort(key=lambda s: (s["weapon"], s["paintKit"]))
manifest = json.dumps({"skins": skins}, ensure_ascii=False, indent=2) + "\n"
mpath = os.path.join(out_dir, manifest_name)
if not os.path.exists(mpath) or open(mpath, encoding="utf-8").read() != manifest:
    with open(mpath, "w", encoding="utf-8") as f:
        f.write(manifest)
print(f"      {len(skins)} skins: {updated} updated, {unchanged} unchanged, {failed} failed; {unmatched} previews with no paint kit (pets, odd names) skipped", flush=True)
`
