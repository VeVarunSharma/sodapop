"""Animate the existing Sodapop artwork; no fonts, accounts, or network are needed."""

import argparse
import json
import math
import os
from pathlib import Path
import re
import sys
import tempfile

from PIL import Image, ImageChops, ImageColor, ImageDraw, ImageFilter, ImageFont


ROOT = Path(__file__).resolve().parent.parent
NAME = "sodapop-title"
WIDTH = 1000
HEIGHT = 560
FRAMES = 80
FRAME_MS = 50
MAX_GIF_BYTES = 2 * 1024 * 1024


def read_manifest(path):
    manifest = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(manifest, dict) or manifest.get("brand") != "Sodapop":
        raise ValueError("The artwork manifest must identify the Sodapop brand.")
    if not isinstance(manifest.get("assets"), list):
        raise ValueError("The artwork manifest must contain an assets list.")
    if any(not isinstance(asset, dict) for asset in manifest["assets"]):
        raise ValueError("Every artwork manifest asset must be an object.")
    if not isinstance(manifest.get("palette"), dict):
        raise ValueError("The artwork manifest must contain a palette object.")
    return manifest


def read_artwork(path):
    with Image.open(path) as source:
        image = source.convert("RGBA")
    bounds = image.getchannel("A").getbbox()
    if bounds is None:
        raise ValueError(f"The artwork is completely transparent: {path.name}")
    return image.crop(bounds)


class Scene:
    def __init__(self, assets=ROOT, width=WIDTH):
        if not isinstance(width, int) or not 160 <= width <= 2000:
            raise ValueError("Artwork width must be an integer between 160 and 2000.")
        self.width = width
        self.height = round(width * HEIGHT / WIDTH)
        self.scale = width / WIDTH
        manifest = read_manifest(Path(assets) / "brand-assets.json")
        self.colors = {}
        for name in ("ink", "pink", "purple", "cyan", "white", "muted", "border"):
            value = manifest.get("palette", {}).get(name)
            if not isinstance(value, str) or not re.fullmatch(r"#[0-9a-fA-F]{6}", value):
                raise ValueError(f"Missing or invalid brand color: {name}")
            self.colors[name] = ImageColor.getrgb(value)
        self.wordmark = self.fit(read_artwork(Path(assets) / "sodapop-wordmark.png"), width=876)
        self.mascot = self.fit(read_artwork(Path(assets) / "sodapop-mascot.png"), height=216)
        self.title_mask = self.wordmark.getchannel("A")
        self.outline = self.title_mask.filter(ImageFilter.MaxFilter(5))
        self.background = self.make_background()
        self.caption = self.make_caption()

    def fit(self, image, *, width=None, height=None):
        factor = self.scale * (width / image.width if width is not None else height / image.height)
        return image.resize(
            (max(1, round(image.width * factor)), max(1, round(image.height * factor))),
            Image.Resampling.LANCZOS,
        )

    def point(self, x, y):
        return round(x * self.scale), round(y * self.scale)

    def box(self, x0, y0, x1, y1):
        return self.point(x0, y0) + self.point(x1, y1)

    def rgba(self, name, alpha=255):
        return (*self.colors[name], round(alpha))

    def layer(self):
        return Image.new("RGBA", (self.width, self.height))

    def make_background(self):
        image = Image.new("RGBA", (self.width, self.height), self.rgba("ink"))
        glow = self.layer()
        draw = ImageDraw.Draw(glow)
        draw.ellipse(self.box(440, 22, 1080, 560), fill=self.rgba("purple", 51))
        draw.ellipse(self.box(-130, 110, 530, 550), fill=self.rgba("pink", 32))
        draw.ellipse(self.box(20, 380, 550, 680), fill=self.rgba("cyan", 21))
        image.alpha_composite(glow.filter(ImageFilter.GaussianBlur(80 * self.scale)))
        detail = self.layer()
        draw = ImageDraw.Draw(detail)
        draw.rounded_rectangle(
            self.box(13, 13, 987, 547), radius=round(23 * self.scale),
            outline=self.rgba("purple", 52), width=max(1, round(self.scale)),
        )
        for i in range(23):
            x = 35 + i * 13
            draw.line(
                [self.point(x, 90), self.point(x + 25, 40)],
                fill=self.rgba("purple" if i % 2 else "pink", 31),
                width=max(1, round(self.scale)),
            )
            draw.line(
                [self.point(663 + i * 13, 512), self.point(688 + i * 13, 462)],
                fill=self.rgba("purple", 29), width=max(1, round(self.scale)),
            )
        for y in range(29, 540, 8):
            draw.line(
                [self.point(25, y), self.point(975, y)], fill=self.rgba("purple", 5),
                width=1,
            )
        draw.arc(
            self.box(662, 291, 974, 541), 194, 342,
            fill=self.rgba("purple", 74), width=max(1, round(self.scale)),
        )
        draw.arc(
            self.box(681, 308, 955, 523), 12, 167,
            fill=self.rgba("cyan", 59), width=max(1, round(self.scale)),
        )
        image.alpha_composite(detail)
        return image

    def make_caption(self):
        image = self.layer()
        draw = ImageDraw.Draw(image)
        font = ImageFont.load_default(size=max(8, round(14 * self.scale)))
        draw.text(self.point(68, 72), "THE TERMINAL CODING COMPANION", font=font, fill=self.rgba("muted"))
        font = ImageFont.load_default(size=max(8, round(27 * self.scale)))
        draw.text(
            self.point(69, 385), "Your terminal, with a little more pop.",
            font=font, fill=self.rgba("white"),
        )
        font = ImageFont.load_default(size=max(8, round(16 * self.scale)))
        draw.text(self.point(70, 470), "sodapop.sh", font=font, fill=self.rgba("cyan"))
        for i in range(5):
            draw.line(
                [self.point(547 + i * 13, 480), self.point(558 + i * 13, 457)],
                fill=self.rgba("pink" if i < 2 else "purple"),
                width=max(1, round(3 * self.scale)),
            )
        return image

    def frame(self, phase):
        if not math.isfinite(phase):
            raise ValueError("Animation phase must be finite.")
        phase %= 1.0
        angle = math.tau * phase
        image = self.background.copy()
        particles = self.layer()
        draw = ImageDraw.Draw(particles)
        for i in range(18):
            progress = (phase + i * 0.381966) % 1
            x = 47 + (i * 149) % 898 + 13 * math.sin(angle + i)
            y = 521 - progress * (300 + i % 4 * 33)
            radius = 3 + i % 5 * 1.9
            opacity = 154 * math.sin(math.pi * progress) ** 1.6
            color = "cyan" if i % 3 else "pink"
            draw.ellipse(
                self.box(x - radius, y - radius, x + radius, y + radius),
                outline=self.rgba(color, opacity),
                width=max(1, round(1.5 * self.scale)),
            )
            if radius > 7:
                draw.arc(
                    self.box(x - radius + 2, y - radius + 2, x + radius - 2, y + radius - 2),
                    204, 262, fill=self.rgba("white", opacity * 0.6),
                    width=max(1, round(self.scale)),
                )
        for i, (x, y) in enumerate(((939, 96), (52, 337), (661, 380), (541, 54))):
            alpha = 75 + 70 * (0.5 + 0.5 * math.sin(angle + i * 1.9))
            length = 4 + 2 * math.sin(angle + i)
            draw.line(
                [self.point(x - length, y), self.point(x + length, y)],
                fill=self.rgba("cyan", alpha), width=max(1, round(self.scale)),
            )
            draw.line(
                [self.point(x, y - length), self.point(x, y + length)],
                fill=self.rgba("cyan", alpha), width=max(1, round(self.scale)),
            )
        for offset, color in ((0, "cyan"), (0.5, "pink")):
            a = angle + offset * math.tau
            x, y = 818 + 145 * math.cos(a), 416 + 108 * math.sin(a)
            draw.ellipse(self.box(x - 3, y - 3, x + 3, y + 3), fill=self.rgba(color, 215))
        image.alpha_composite(particles)

        mascot = self.mascot.rotate(2.4 * math.sin(angle), Image.Resampling.BICUBIC, expand=True)
        x, y = self.point(817, 430 - 4 * math.sin(angle))
        image.alpha_composite(mascot, (x - mascot.width // 2, y - mascot.height // 2))

        x, y = self.point(62, 127 + 2 * math.sin(angle))
        shadow = Image.new("RGBA", self.wordmark.size, self.rgba("purple", 0))
        shadow.putalpha(self.outline.point(lambda value: round(value * 0.20)))
        halo = Image.new("RGBA", self.wordmark.size, self.rgba("pink", 0))
        halo.putalpha(self.title_mask.filter(ImageFilter.GaussianBlur(9 * self.scale)).point(lambda value: round(value * 0.23)))
        image.alpha_composite(halo, (x, y))
        for depth in range(8, 1, -2):
            image.alpha_composite(shadow, (x + round(depth * self.scale), y + round(depth * 1.3 * self.scale)))
        edge = Image.new("RGBA", self.wordmark.size, self.rgba("cyan", 0))
        edge.putalpha(self.outline.point(lambda value: round(value * 0.28)))
        image.alpha_composite(edge, (x + round(2 * self.scale), y + round(3 * self.scale)))
        image.alpha_composite(self.wordmark, (x, y))

        sweep = Image.new("L", self.wordmark.size)
        center = (phase * 1.5 - 0.25) * self.wordmark.width
        band = 68 * self.scale
        draw = ImageDraw.Draw(sweep)
        for step in range(12):
            left = center + step * band / 12
            alpha = round(58 * math.sin(math.pi * step / 12))
            draw.polygon(
                [(left, 0), (left + band / 12 + 1, 0),
                 (left - band + band / 12 + 1, self.wordmark.height), (left - band, self.wordmark.height)],
                fill=alpha,
            )
        highlight = Image.new("RGBA", self.wordmark.size, self.rgba("white", 0))
        highlight.putalpha(ImageChops.multiply(self.title_mask, sweep))
        image.alpha_composite(highlight, (x, y))
        image.alpha_composite(self.caption)
        return image.convert("RGB")


def encode(scene, destination, frames=FRAMES, frame_ms=FRAME_MS):
    if not isinstance(frames, int) or not 2 <= frames <= 160:
        raise ValueError("An animation needs between 2 and 160 frames.")
    if not isinstance(frame_ms, int) or frame_ms < 20 or frame_ms % 10:
        raise ValueError("GIF frame duration must be a multiple of 10 ms, at least 20 ms.")
    rendered = [scene.frame(index / frames) for index in range(frames)]
    # A shared palette avoids color flicker and lets the GIF encoder store small frame deltas.
    sample = Image.new("RGB", (500, 280 * 2))
    for index in range(8):
        thumbnail = rendered[min(frames - 1, index * frames // 8)].resize((250, 140), Image.Resampling.LANCZOS)
        sample.paste(thumbnail, ((index % 2) * 250, (index // 2) * 140))
    palette = sample.quantize(colors=255, method=Image.Quantize.MEDIANCUT)
    indexed = [image.quantize(palette=palette, dither=Image.Dither.NONE) for image in rendered]
    indexed[0].save(
        destination / f"{NAME}.gif", save_all=True, append_images=indexed[1:],
        duration=frame_ms, loop=0, disposal=1, optimize=True,
    )
    rendered[0].save(destination / f"{NAME}.png", optimize=True)
    with Image.open(destination / f"{NAME}.gif") as animation:
        duration = 0
        for index in range(animation.n_frames):
            animation.seek(index)
            animation.load()
            duration += animation.info["duration"]
        if animation.size != (scene.width, scene.height) or animation.info.get("loop") != 0:
            raise ValueError("The encoded title has incorrect dimensions or looping behavior.")
        if animation.n_frames < 2 or duration != frames * frame_ms:
            raise ValueError("The encoded title lost frames or animation timing.")
        frame_count = animation.n_frames
    return {
        "name": NAME,
        "title": "Sodapop animated title",
        "use": "Looping README title using the original wordmark and mascot. Use the PNG for reduced motion.",
        "width": scene.width, "height": scene.height, "transparent": False,
        "gif": f"{NAME}.gif", "png": f"{NAME}.png",
        "gifBytes": (destination / f"{NAME}.gif").stat().st_size,
        "pngBytes": (destination / f"{NAME}.png").stat().st_size,
        "frames": frame_count, "durationMs": duration, "loop": True,
        "source": "source/animate.py",
        "inputs": ["sodapop-wordmark.png", "sodapop-mascot.png"],
        "typography": "Existing outlined wordmark; Pillow's embedded font for the secondary caption. No external fonts.",
    }


def export(scene, output, frames=FRAMES, frame_ms=FRAME_MS, max_bytes=MAX_GIF_BYTES):
    output = Path(output)
    catalog = output / "brand-assets.json"
    manifest = read_manifest(catalog) if catalog.exists() else None
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".sodapop-title-", dir=output) as temporary:
        stage = Path(temporary)
        entry = encode(scene, stage, frames, frame_ms)
        if entry["gifBytes"] > max_bytes:
            raise ValueError(
                f"The title GIF is {entry['gifBytes'] // 1024} KiB, above the "
                f"{max_bytes // 1024} KiB budget; existing artwork was not replaced."
            )
        names = [f"{NAME}.gif", f"{NAME}.png"]
        if manifest is not None:
            manifest["assets"] = [entry] + [asset for asset in manifest["assets"] if asset.get("name") != NAME]
            (stage / catalog.name).write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
            names.append(catalog.name)
        for name in names:
            os.replace(stage / name, output / name)
    return entry


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assets", type=Path, default=ROOT, help="directory containing the existing Sodapop brand assets")
    parser.add_argument("--output", type=Path, default=ROOT, help="destination for the GIF and static PNG")
    args = parser.parse_args()
    entry = export(Scene(args.assets), args.output)
    print(f"Rendered {args.output / entry['gif']}: {entry['width']}x{entry['height']}, "
          f"{entry['durationMs'] / 1000:.2f}s, {entry['gifBytes'] / 1024:.1f} KiB")
    print(f"Static alternative: {args.output / entry['png']}")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"sodapop title: {error}", file=sys.stderr)
        sys.exit(1)
