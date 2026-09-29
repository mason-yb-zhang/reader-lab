"""Build/check C1P1 native 12px glyphs; derived font remains under bundled OFL 1.1."""
from __future__ import annotations

import argparse
import hashlib
from pathlib import Path
import shutil
import struct

from fontTools.ttLib import TTFont
from PIL import Image, ImageDraw, ImageFont

READER = Path(__file__).resolve().parents[1]
SOURCE = READER.parents[1] / "assets/fusion-pixel-12px"
TTF = SOURCE / "fusion-pixel-12px-proportional-zh_hans.ttf"
SOURCE_SHA256 = "1b423de0be589d159ef71af7d00530a7176e16af768a26adbc2e524e8817aad9"
OUTPUT = READER / "assets/fusion12.bin"
LICENSES = READER / "assets/fusion12-licenses"
CLIPPED: dict[int, tuple[int, ...]] = {}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def source_cmap() -> set[int]:
    with TTFont(TTF) as font:
        return {cp for table in font["cmap"].tables if table.isUnicode()
                for cp in table.cmap if 0 <= cp <= 0x10FFFF and not 0xD800 <= cp <= 0xDFFF}


def raster(font: ImageFont.FreeTypeFont, cp: int) -> tuple[int, Image.Image]:
    char = chr(cp)
    width = min(16, max(1, round(font.getlength(char))))
    if cp >= 0x2E80:
        width = max(12, width)
    image = Image.new("1", (16, 16))
    ImageDraw.Draw(image).text((0, -3), char, font=font, fill=1, anchor="la")
    # A padded independent render detects clipping, including descenders/accents.
    padded = Image.new("1", (64, 64))
    ImageDraw.Draw(padded).text((24, 21), char, font=font, fill=1, anchor="la")
    bbox = padded.getbbox()
    if bbox and not (24 <= bbox[0] <= bbox[2] <= 40 and 24 <= bbox[1] <= bbox[3] <= 40):
        CLIPPED[cp] = tuple(v - 24 for v in bbox)
    if padded.crop((24, 24, 40, 40)).tobytes() != image.tobytes():
        raise ValueError(f"U+{cp:04X} padded raster mismatch")
    return width, image


def replacement() -> tuple[int, Image.Image]:
    image = Image.new("1", (16, 16))
    draw = ImageDraw.Draw(image)
    draw.rectangle((1, 1, 10, 11), outline=1)
    draw.line((3, 3, 8, 9), fill=1)
    draw.line((8, 3, 3, 9), fill=1)
    return 12, image


def build() -> tuple[bytes, int, bool]:
    if sha256(TTF.read_bytes()) != SOURCE_SHA256:
        raise ValueError("Unexpected Fusion Pixel source SHA256")
    cmap = source_cmap()
    font = ImageFont.truetype(str(TTF), 12)
    synthesized = 0xFFFD not in cmap
    result = bytearray(b"C1P1")
    CLIPPED.clear()
    for cp in sorted(cmap | {0xFFFD}):
        width, image = replacement() if cp == 0xFFFD and synthesized else raster(font, cp)
        if cp == 0xFFFD and (cp in CLIPPED or image.getbbox() is None):
            width, image = replacement()
            synthesized = True
        elif cp in CLIPPED:
            continue
        rows = [sum(0x8000 >> x for x in range(16) if image.getpixel((x, y))) for y in range(16)]
        result.extend(struct.pack("<IB", cp, width))
        result.extend(struct.pack(">16H", *rows))
    return bytes(result), len(cmap), synthesized


def verify(data: bytes) -> None:
    if sha256(TTF.read_bytes()) != SOURCE_SHA256:
        raise ValueError("Unexpected Fusion Pixel source SHA256")
    cmap = source_cmap()
    codepoints = sorted(cmap | {0xFFFD})
    if data[:4] != b"C1P1" or len(data) <= 4 or (len(data) - 4) % 37:
        raise ValueError("Asset header/length/cmap coverage mismatch")
    font = ImageFont.truetype(str(TTF), 12)
    synthesized = 0xFFFD not in cmap
    CLIPPED.clear()
    offset = 4
    for cp in codepoints:
        width, image = replacement() if cp == 0xFFFD and synthesized else raster(font, cp)
        if cp == 0xFFFD and (cp in CLIPPED or image.getbbox() is None):
            width, image = replacement()
            synthesized = True
        elif cp in CLIPPED:
            continue
        record = data[offset:offset + 37]
        offset += 37
        if len(record) != 37:
            raise ValueError(f"Missing retained glyph U+{cp:04X}")
        # Pillow's packed 1-bit rows are independent of the encoder's bit loop.
        if int.from_bytes(record[:4], "little") != cp or record[4] != width or record[5:] != image.tobytes():
            raise ValueError(f"U+{cp:04X} source raster/advance mismatch")
    if offset != len(data):
        raise ValueError("Asset contains extra or excluded glyph records")
    source_count = len(cmap)
    sources = [SOURCE / "OFL.txt", *sorted((SOURCE / "LICENSES").rglob("*"))]
    for source in sources:
        if source.is_file() and (LICENSES / source.relative_to(SOURCE)).read_bytes() != source.read_bytes():
            raise ValueError(f"License mismatch: {source}")
    print(f"source_sha256={SOURCE_SHA256}")
    print(f"asset_sha256={sha256(data)} bytes={len(data)} glyphs={(len(data)-4)//37}")
    print(f"source_scalar_glyphs={source_count} synthesized_replacement={synthesized}")
    print("PASS: every retained glyph pixel/advance, no retained clipping, complete license bytes")
    excluded = sorted(set(CLIPPED) - {0xFFFD})
    print(f"excluded_glyphs={len(excluded)}")
    print("excluded_codepoints=" + ",".join(f"U+{cp:04X}" for cp in excluded))


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Verify existing artifact and licenses without writing")
    args = parser.parse_args()
    if not args.check:
        data, _, _ = build()
        OUTPUT.parent.mkdir(parents=True, exist_ok=True)
        OUTPUT.write_bytes(data)
        LICENSES.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(SOURCE / "OFL.txt", LICENSES / "OFL.txt")
        shutil.copytree(SOURCE / "LICENSES", LICENSES / "LICENSES", dirs_exist_ok=True)
    verify(OUTPUT.read_bytes())


if __name__ == "__main__":
    main()
