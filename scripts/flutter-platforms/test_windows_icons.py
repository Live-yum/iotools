"""Structural icon checks; no Flutter SDK or Windows execution required."""
import importlib.util
from pathlib import Path
import struct
import unittest
import zlib

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("windows_icons", HERE / "verify-windows-icons.py")
icons = importlib.util.module_from_spec(spec)
spec.loader.exec_module(icons)


def png(size, *, opaque=False, erase_hat=False):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xffffffff)

    pixels = bytearray(bytes((8, 20, 31, 255) if opaque else (0, 0, 0, 0)) * size * size)
    for y in range(size // 3, size * 3 // 4):
        for x in range(size // 4, size * 3 // 4):
            i = (y * size + x) * 4
            pixels[i:i + 4] = bytes((40, 100, 190, 255))
    x, y = int(size * .5), int(size * (.125 + .75 * 96 / 512))
    i = (y * size + x) * 4
    pixels[i:i + 4] = bytes((20, 22, 24, 0 if erase_hat else 255))
    raw = b"".join(b"\0" + pixels[y * size * 4:(y + 1) * size * 4] for y in range(size))
    return (icons.PNG_SIGNATURE + chunk(b"IHDR", struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def make_ico(frames):
    header = bytearray(struct.pack("<HHH", 0, 1, len(frames)))
    offset = 6 + 16 * len(frames)
    for size, data in frames:
        header.extend(struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(data), offset))
        offset += len(data)
    return bytes(header) + b"".join(data for _, data in frames)


def make_pe(frames):
    # A minimal, non-executable PE file with the same standard resource tree
    # (RT_ICON IDs 1..4 and RT_GROUP_ICON ID 101 / language 1033).
    group = bytearray(struct.pack("<HHH", 0, 1, len(frames)))
    for i, (size, payload) in enumerate(frames, 1):
        group.extend(struct.pack("<BBBBHHIH", size % 256, size % 256, 0, 0, 1, 32, len(payload), i))
    tree = {3: {i: {1033: data} for i, (_, data) in enumerate(frames, 1)}, 14: {101: {1033: bytes(group)}}}
    resource = bytearray()

    def directory(children):
        start = len(resource)
        resource.extend(struct.pack("<IIHHHH", 0, 0, 0, 0, 0, len(children)) + bytes(8 * len(children)))
        for i, (key, child) in enumerate(children.items()):
            if isinstance(child, dict):
                value = directory(child) | 0x80000000
            else:
                value = len(resource)
                resource.extend(struct.pack("<IIII", 0x1000 + value + 16, len(child), 0, 0))
                resource.extend(child)
            struct.pack_into("<II", resource, start + 16 + i * 8, key, value)
        return start

    directory(tree)
    result = bytearray(0x200)
    result[:2] = b"MZ"
    struct.pack_into("<I", result, 0x3c, 0x80)
    result[0x80:0x84] = b"PE\0\0"
    struct.pack_into("<HHIIIHH", result, 0x84, 0x8664, 1, 0, 0, 0, 240, 0)
    optional = 0x98
    struct.pack_into("<H", result, optional, 0x20b)
    struct.pack_into("<II", result, optional + 112 + 16, 0x1000, len(resource))
    section = optional + 240
    result[section:section + 8] = b".rsrc\0\0\0"
    struct.pack_into("<IIII", result, section + 8, len(resource), 0x1000, len(resource), 0x200)
    return bytes(result) + resource


class WindowsIconTests(unittest.TestCase):
    def frames(self, **kwargs):
        return [(size, png(size, **kwargs)) for size in icons.SIZES]

    def test_transparent_ico_and_compiled_resources_match(self):
        frames = self.frames()
        ico = icons.ico_frames(make_ico(frames))
        pe = icons.pe_frames(make_pe(frames))
        self.assertEqual(pe, ico)
        self.assertTrue(all(not frame["issues"] for frame in icons.inspect_frames(pe)))

    def test_opaque_background_is_rejected_at_every_size(self):
        frames = icons.pe_frames(make_pe(self.frames(opaque=True)))
        for frame in icons.inspect_frames(frames):
            self.assertIn("surrounding corner pixels are not transparent", frame["issues"])
            self.assertEqual(frame["transparent_pixels"], 0)

    def test_removing_dark_beret_is_rejected(self):
        frames = icons.ico_frames(make_ico(self.frames(erase_hat=True)))
        for frame in icons.inspect_frames(frames):
            self.assertIn("original dark beret must remain opaque", frame["issues"])

    def test_png_crc_and_truncation_are_rejected(self):
        valid = png(16)
        for invalid in [valid[:-1], valid[:40] + bytes([valid[40] ^ 1]) + valid[41:]]:
            with self.assertRaises(ValueError):
                icons.png_rgba(invalid)

    def test_ico_bounds_and_wrong_dimensions_are_rejected(self):
        data = bytearray(make_ico(self.frames()))
        struct.pack_into("<I", data, 18, len(data) + 1)
        with self.assertRaisesRegex(ValueError, "bounds"):
            icons.ico_frames(data)
        data = bytearray(make_ico(self.frames()))
        data[6] = 15
        with self.assertRaisesRegex(ValueError, "sizes"):
            icons.inspect_frames(icons.ico_frames(data))

    def test_pe_missing_group_and_resource_bounds_are_rejected(self):
        data = bytearray(make_pe(self.frames()))
        struct.pack_into("<I", data, 0x98 + 112 + 16, 0x9000)
        with self.assertRaisesRegex(ValueError, "file-backed"):
            icons.pe_frames(data)
        with self.assertRaises(ValueError):
            icons.pe_frames(b"MZ" + bytes(62))

    def test_original_avatar_keeps_transparent_surroundings_and_opaque_hat(self):
        master = HERE.parents[1] / "mobile/android/app/src/main/res/drawable-nodpi/iotools_avatar.png"
        w, h, rgba = icons.png_rgba(master.read_bytes())
        self.assertEqual((w, h), (512, 512))
        self.assertEqual(rgba[3], 0)
        self.assertEqual(rgba[3::4].count(0), 87491)
        offset = (96 * w + 256) * 4
        self.assertEqual(list(rgba[offset:offset + 4]), [22, 23, 25, 255])


if __name__ == "__main__":
    unittest.main()
