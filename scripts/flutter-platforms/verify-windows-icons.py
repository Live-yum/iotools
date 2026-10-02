#!/usr/bin/env python3
"""Check actual Windows EXE/ICO PNG frames, including alpha and the black beret.

Uses only the standard library; never executes the inspected Windows binary.
"""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import sys
import zlib

SIZES = (16, 32, 48, 256)
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"


def require(condition, message):
    if not condition:
        raise ValueError(message)


def ico_frames(data):
    require(len(data) >= 6, "truncated ICO header")
    reserved, kind, count = struct.unpack_from("<HHH", data)
    require((reserved, kind, count) == (0, 1, len(SIZES)), "expected four ICO images")
    require(len(data) >= 6 + count * 16, "truncated ICO table")
    frames = []
    for i in range(count):
        w, h, colors, reserved, planes, bits, size, offset = struct.unpack_from(
            "<BBBBHHII", data, 6 + i * 16)
        require(planes == 1 and bits == 32, "expected 32-bit ICO image")
        require(offset >= 6 + count * 16 and offset + size <= len(data), "invalid ICO image bounds")
        frames.append((w or 256, h or 256, data[offset:offset + size]))
    return frames


def pe_frames(data):
    require(len(data) >= 64 and data[:2] == b"MZ", "expected Windows executable")
    pe = struct.unpack_from("<I", data, 0x3c)[0]
    require(pe + 24 <= len(data) and data[pe:pe + 4] == b"PE\0\0", "invalid PE header")
    coff = pe + 4
    count = struct.unpack_from("<H", data, coff + 2)[0]
    optional_size = struct.unpack_from("<H", data, coff + 16)[0]
    optional = coff + 20
    require(optional + optional_size + count * 40 <= len(data), "truncated PE sections")
    magic = struct.unpack_from("<H", data, optional)[0]
    require(magic in (0x10b, 0x20b), "unsupported PE optional header")
    directories = 112 if magic == 0x20b else 96
    require(optional_size >= directories + 24, "missing PE resource directory")
    resource_rva, resource_size = struct.unpack_from("<II", data, optional + directories + 16)
    sections = []
    for i in range(count):
        virtual_size, address, raw_size, raw = struct.unpack_from(
            "<IIII", data, optional + optional_size + i * 40 + 8)
        sections.append((address, raw_size, raw))

    def at(rva, length):
        for address, raw_size, raw in sections:
            if address <= rva and rva + length <= address + raw_size:
                offset = raw + rva - address
                require(offset + length <= len(data), "resource exceeds executable")
                return offset
        raise ValueError("resource is outside a file-backed PE section")

    base = at(resource_rva, resource_size)
    leaves = {}
    visited = set()

    def resource(offset, length):
        require(0 <= offset and offset + length <= resource_size, "resource directory bounds")
        return base + offset

    def walk(relative, path=()):
        require(len(path) <= 2 and relative not in visited, "invalid resource directory tree")
        visited.add(relative)
        offset = resource(relative, 16)
        named, ids = struct.unpack_from("<HH", data, offset + 12)
        require(named + ids <= 1024, "resource directory too large")
        resource(relative + 16, (named + ids) * 8)
        for i in range(named + ids):
            name, value = struct.unpack_from("<II", data, offset + 16 + i * 8)
            if name & 0x80000000:
                continue  # Icon and icon-group resources use integer IDs.
            key = path + (name,)
            if value & 0x80000000:
                walk(value & 0x7fffffff, key)
            else:
                require(len(key) == 3, "invalid resource leaf")
                rva, size, _, _ = struct.unpack_from("<IIII", data, resource(value, 16))
                offset_data = at(rva, size)
                require(key not in leaves, "duplicate resource leaf")
                leaves[key] = data[offset_data:offset_data + size]

    walk(0)
    groups = [(key, value) for key, value in leaves.items() if key[:2] == (14, 101)]
    require(len(groups) == 1, "expected one IDI_APP_ICON (101) resource group")
    key, group = groups[0]
    require(len(group) >= 6, "truncated icon group")
    reserved, kind, count = struct.unpack_from("<HHH", group)
    require((reserved, kind, count) == (0, 1, len(SIZES)), "expected four grouped icon images")
    require(len(group) == 6 + 14 * count, "invalid icon group length")
    frames = []
    for i in range(count):
        w, h, _, _, planes, bits, size, image_id = struct.unpack_from("<BBBBHHIH", group, 6 + 14 * i)
        require(planes == 1 and bits == 32, "expected 32-bit executable icon")
        image = leaves.get((3, image_id, key[2]))
        require(image is not None and len(image) == size, "missing/mismatched RT_ICON payload")
        frames.append((w or 256, h or 256, image))
    return frames


def png_rgba(data):
    require(data.startswith(PNG_SIGNATURE), "Windows icon must contain PNG-compressed RGBA")
    offset = len(PNG_SIGNATURE)
    compressed = bytearray()
    width = height = None
    finished = False
    while offset < len(data):
        require(offset + 12 <= len(data), "truncated PNG chunk")
        length, = struct.unpack_from(">I", data, offset)
        require(offset + 12 + length <= len(data), "invalid PNG chunk length")
        kind = data[offset + 4:offset + 8]
        payload = data[offset + 8:offset + 8 + length]
        crc, = struct.unpack_from(">I", data, offset + 8 + length)
        require(zlib.crc32(kind + payload) & 0xffffffff == crc, "PNG CRC mismatch")
        if kind == b"IHDR":
            require(width is None and length == 13, "invalid PNG header")
            width, height, bits, color, compression, filtering, interlace = struct.unpack(">IIBBBBB", payload)
            require(0 < width <= 1024 and 0 < height <= 1024, "PNG dimensions out of bounds")
            require((bits, color, compression, filtering, interlace) == (8, 6, 0, 0, 0),
                    "expected noninterlaced 8-bit RGBA PNG")
        elif kind == b"IDAT":
            compressed.extend(payload)
        elif kind == b"IEND":
            require(length == 0 and offset + 12 == len(data), "invalid PNG end")
            finished = True
            break
        offset += 12 + length
    require(width is not None and finished, "incomplete PNG")
    expected = height * (width * 4 + 1)
    inflater = zlib.decompressobj()
    raw = inflater.decompress(compressed, expected + 1)
    require(len(raw) == expected and inflater.eof and not inflater.unused_data, "invalid PNG pixel stream")
    pixels = bytearray()
    previous = bytearray(width * 4)
    stride = width * 4
    for y in range(height):
        kind = raw[y * (stride + 1)]
        require(kind <= 4, "unsupported PNG row filter")
        row = bytearray(raw[y * (stride + 1) + 1:(y + 1) * (stride + 1)])
        for x in range(stride):
            left = row[x - 4] if x >= 4 else 0
            up = previous[x]
            upper_left = previous[x - 4] if x >= 4 else 0
            predictor = 0
            if kind == 1:
                predictor = left
            elif kind == 2:
                predictor = up
            elif kind == 3:
                predictor = (left + up) // 2
            elif kind == 4:
                p = left + up - upper_left
                distances = (abs(p - left), abs(p - up), abs(p - upper_left))
                predictor = (left, up, upper_left)[distances.index(min(distances))]
            row[x] = (row[x] + predictor) & 255
        pixels.extend(row)
        previous = row
    return width, height, pixels


def inspect_frames(frames):
    require(sorted((w, h) for w, h, _ in frames) == [(n, n) for n in SIZES], "wrong Windows icon sizes")
    report = []
    for size, height, data in frames:
        w, h, pixels = png_rgba(data)
        require((w, h) == (size, height), "icon table/PNG dimensions differ")
        alpha = pixels[3::4]

        def pixel(x, y):
            return list(pixels[(y * w + x) * 4:(y * w + x) * 4 + 4])

        corners = [pixel(x, y) for x, y in ((0, 0), (w - 1, 0), (0, h - 1), (w - 1, h - 1))]
        hat = pixel(int(w * .5), int(h * (.125 + .75 * 96 / 512)))
        issues = []
        if any(p[3] != 0 for p in corners):
            issues.append("surrounding corner pixels are not transparent")
        if alpha.count(0) <= w * h // 4:
            issues.append("transparent padding is missing")
        if hat[3] != 255 or max(hat[:3]) >= 80:
            issues.append("original dark beret must remain opaque")
        report.append({"size": size, "sha256": hashlib.sha256(data).hexdigest(),
                       "encoding": "RGBA PNG; no DIB AND mask", "corners_rgba": corners,
                       "transparent_pixels": alpha.count(0), "opaque_pixels": alpha.count(255),
                       "partial_alpha_pixels": sum(0 < a < 255 for a in alpha),
                       "beret_rgba": hat, "issues": issues})
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path, help="Built iotools.exe or generated app_icon.ico")
    parser.add_argument("--expected-ico", type=Path, help="Also require exact generated ICO frame bytes in the EXE")
    parser.add_argument("--extract-dir", type=Path, help="Optional directory for exact embedded PNG frames")
    parser.add_argument("--report", type=Path, help="Write the JSON result")
    args = parser.parse_args()
    data = args.input.read_bytes()
    frames = pe_frames(data) if data[:2] == b"MZ" else ico_frames(data)
    if args.expected_ico:
        expected = {w: raw for w, _, raw in ico_frames(args.expected_ico.read_bytes())}
        require({w: raw for w, _, raw in frames} == expected, "compiled EXE icons differ from generated ICO")
    report = {"input": str(args.input), "sha256": hashlib.sha256(data).hexdigest(), "frames": inspect_frames(frames)}
    report["passed"] = all(not frame["issues"] for frame in report["frames"])
    if args.extract_dir:
        args.extract_dir.mkdir(parents=True, exist_ok=True)
        for size, _, raw in frames:
            (args.extract_dir / f"icon-{size}.png").write_bytes(raw)
    result = json.dumps(report, indent=2) + "\n"
    if args.report:
        args.report.write_text(result, encoding="utf-8")
    print(result, end="")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, struct.error, OSError, zlib.error) as error:
        print(f"Windows icon validation failed: {error}", file=sys.stderr)
        sys.exit(1)
