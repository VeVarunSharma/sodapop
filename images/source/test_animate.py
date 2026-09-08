import json
from pathlib import Path
import tempfile
import unittest

from PIL import Image, ImageChops, ImageDraw

from animate import NAME, Scene, export


class TitleAnimationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.manifest = {
            "brand": "Sodapop",
            "palette": {
                "ink": "#09090B", "pink": "#FB7185", "purple": "#C084FC",
                "cyan": "#67E8F9", "white": "#F4F4F5", "muted": "#9292A3",
                "border": "#30303D",
            },
            "assets": [{"name": "existing", "use": "must be preserved"}],
            "typography": "Existing brand typography",
        }
        self.save_manifest()
        word = Image.new("RGBA", (220, 64))
        draw = ImageDraw.Draw(word)
        draw.rectangle((8, 8, 103, 58), fill="#FB7185")
        draw.rectangle((112, 8, 210, 58), fill="#C084FC")
        word.save(self.root / "sodapop-wordmark.png")
        mascot = Image.new("RGBA", (80, 100))
        ImageDraw.Draw(mascot).rounded_rectangle((10, 5, 70, 95), 12, fill="#C084FC", outline="#67E8F9", width=4)
        mascot.save(self.root / "sodapop-mascot.png")

    def save_manifest(self):
        (self.root / "brand-assets.json").write_text(json.dumps(self.manifest), encoding="utf-8")

    def test_scene_is_periodic_deterministic_and_keeps_brand_proportions(self):
        scene = Scene(self.root, width=240)
        initial = scene.frame(0)
        self.assertEqual(initial.size, (240, 134))
        self.assertIsNone(ImageChops.difference(initial, scene.frame(1)).getbbox())
        self.assertIsNone(ImageChops.difference(initial, scene.frame(0)).getbbox())
        self.assertIsNotNone(ImageChops.difference(initial, scene.frame(0.25)).getbbox())
        self.assertAlmostEqual(scene.wordmark.width / scene.wordmark.height, 203 / 51, delta=0.08)
        with self.assertRaisesRegex(ValueError, "finite"):
            scene.frame(float("nan"))

    def test_export_has_real_animation_still_and_preserves_catalog_and_inputs(self):
        scene = Scene(self.root, width=240)
        inputs = {path.name: path.read_bytes() for path in self.root.glob("*.png")}
        entry = export(scene, self.root, frames=8)
        with Image.open(self.root / entry["gif"]) as animation:
            self.assertEqual(animation.size, (240, 134))
            self.assertEqual(animation.info["loop"], 0)
            self.assertGreater(animation.n_frames, 1)
            duration = 0
            first = animation.convert("RGB").copy()
            for index in range(animation.n_frames):
                animation.seek(index)
                animation.load()
                self.assertEqual(animation.convert("RGBA").getchannel("A").getextrema(), (255, 255))
                duration += animation.info["duration"]
            self.assertEqual(duration, 400)
            self.assertIsNotNone(ImageChops.difference(first, animation.convert("RGB")).getbbox())
        with Image.open(self.root / entry["png"]) as still:
            self.assertEqual(still.size, (240, 134))
            self.assertIsNone(ImageChops.difference(still.convert("RGB"), scene.frame(0)).getbbox())
        manifest = json.loads((self.root / "brand-assets.json").read_text())
        self.assertIn(self.manifest["assets"][0], manifest["assets"])
        self.assertEqual(manifest["typography"], self.manifest["typography"])
        self.assertEqual(entry["durationMs"], 400)
        self.assertEqual(entry["gifBytes"], (self.root / entry["gif"]).stat().st_size)
        for name, data in inputs.items():
            self.assertEqual((self.root / name).read_bytes(), data)
        first_export = (self.root / entry["gif"]).read_bytes()
        export(scene, self.root, frames=8)
        self.assertEqual((self.root / entry["gif"]).read_bytes(), first_export)
        manifest = json.loads((self.root / "brand-assets.json").read_text())
        self.assertEqual(sum(asset["name"] == NAME for asset in manifest["assets"]), 1)
        self.assertEqual(list(self.root.glob(".sodapop-title-*")), [])

    def test_preview_does_not_mutate_the_source_catalog(self):
        before = (self.root / "brand-assets.json").read_bytes()
        output = self.root / "preview"
        export(Scene(self.root, width=240), output, frames=4)
        self.assertEqual((self.root / "brand-assets.json").read_bytes(), before)
        self.assertEqual(sorted(path.name for path in output.iterdir()), [f"{NAME}.gif", f"{NAME}.png"])

    def test_bad_inputs_and_failed_export_do_not_replace_published_artwork(self):
        scene = Scene(self.root, width=240)
        for extension in ("gif", "png"):
            (self.root / f"{NAME}.{extension}").write_bytes(b"existing artwork")
        before = (self.root / "brand-assets.json").read_bytes()
        for options in ({"frames": 1}, {"frame_ms": 25}, {"max_bytes": 1}):
            with self.subTest(options=options), self.assertRaises(ValueError):
                export(scene, self.root, frames=options.get("frames", 4), **{key: value for key, value in options.items() if key != "frames"})
            for extension in ("gif", "png"):
                self.assertEqual((self.root / f"{NAME}.{extension}").read_bytes(), b"existing artwork")
            self.assertEqual((self.root / "brand-assets.json").read_bytes(), before)
            self.assertEqual(list(self.root.glob(".sodapop-title-*")), [])

    def test_invalid_palette_artwork_and_catalog_are_explicit_errors(self):
        with self.assertRaisesRegex(ValueError, "width"):
            Scene(self.root, width=0)
        self.manifest["palette"]["pink"] = "not a color"
        self.save_manifest()
        with self.assertRaisesRegex(ValueError, "pink"):
            Scene(self.root, width=240)
        self.manifest["palette"]["pink"] = "#FB7185"
        self.save_manifest()
        Image.new("RGBA", (20, 20)).save(self.root / "sodapop-wordmark.png")
        with self.assertRaisesRegex(ValueError, "transparent"):
            Scene(self.root, width=240)
        (self.root / "sodapop-wordmark.png").unlink()
        with self.assertRaises(OSError):
            Scene(self.root, width=240)
        self.manifest["brand"] = "Another brand"
        self.save_manifest()
        with self.assertRaisesRegex(ValueError, "Sodapop"):
            Scene(self.root, width=240)
        self.manifest["brand"] = "Sodapop"
        self.manifest["palette"] = None
        self.save_manifest()
        with self.assertRaisesRegex(ValueError, "palette"):
            Scene(self.root, width=240)
        self.manifest["palette"] = {}
        self.manifest["assets"] = [None]
        self.save_manifest()
        with self.assertRaisesRegex(ValueError, "object"):
            Scene(self.root, width=240)


if __name__ == "__main__":
    unittest.main()
