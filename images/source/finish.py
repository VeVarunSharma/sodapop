"""Export web formats and inspect the generated artwork, without application dependencies."""

import json
import math
import re
from html.parser import HTMLParser
from pathlib import Path
from xml.etree import ElementTree

from PIL import Image


ROOT = Path(__file__).resolve().parent.parent
manifest_path = ROOT / "brand-assets.json"
manifest = json.loads(manifest_path.read_text())

for asset in manifest["assets"]:
    if "png" not in asset:
        continue
    with Image.open(ROOT / asset["png"]) as image:
        image.load()
        assert image.size == (asset["width"], asset["height"]), asset["name"]
        alpha = image.convert("RGBA").getchannel("A")
        if asset.get("transparent"):
            assert alpha.getextrema() == (0, 255), asset["name"]
            bounds = alpha.getbbox()
            assert bounds is not None, asset["name"]
        elif "transparent" in asset:
            assert alpha.getextrema() == (255, 255), asset["name"]
    if "svg" not in asset:
        continue
    document = ElementTree.parse(ROOT / asset["svg"])
    ids = [element.attrib["id"] for element in document.iter() if "id" in element.attrib]
    assert len(ids) == len(set(ids)) and all(ids), asset["name"]
    for element in document.iter():
        tag = element.tag.split("}")[-1]
        assert tag not in ("text", "image", "script", "foreignObject"), (asset["name"], tag)
        for key, value in element.attrib.items():
            for reference in re.findall(r"url\(#([^)]*)\)", value):
                assert reference in ids, (asset["name"], reference)
            if key.split("}")[-1] == "href":
                assert value.startswith("#") and value[1:] in ids, (asset["name"], value)

for filename in ("sodapop-oauth.png", "sodapop-repo-social.png"):
    assert (ROOT / filename).stat().st_size < 1_000_000, filename

with Image.open(ROOT / "sodapop-logo.png") as logo:
    alpha = logo.convert("RGBA").getchannel("A")
    for y in range(alpha.height):
        for x in range(alpha.width):
            if alpha.getpixel((x, y)) > 16:
                assert math.hypot(x - 512, y - 512) < 448, "Logo is not circular-crop safe."

with Image.open(ROOT / "sodapop-wordmark.png") as wordmark:
    pixels = wordmark.convert("RGBA")
    for start, end, pink in ((50, 320, True), (880, 1150, False)):
        colors = [pixels.getpixel((x, y)) for x in range(start, end) for y in range(340)
                  if pixels.getpixel((x, y))[3] > 240]
        assert colors, "Wordmark has no visible lettering."
        red = sum(color[0] for color in colors) / len(colors)
        blue = sum(color[2] for color in colors) / len(colors)
        assert (red > blue) == pink, "Wordmark lost its pink-to-purple gradient."

for name in ("sodapop-main", "sodapop-banner", "sodapop-mascot"):
    with Image.open(ROOT / f"{name}.png") as image:
        destination = ROOT / f"{name}.webp"
        image.save(destination, "WEBP", lossless=True, method=6, exact=True)
        with Image.open(destination) as exported:
            assert exported.size == image.size, name
            assert exported.convert("RGBA").tobytes() == image.convert("RGBA").tobytes(), name
    for asset in manifest["assets"]:
        if asset["name"] == name:
            asset["webp"] = destination.name
            asset["webpBytes"] = destination.stat().st_size

with Image.open(ROOT / "sodapop-favicon.png") as icon:
    icon.save(ROOT / "favicon.ico", format="ICO", sizes=[(16, 16), (32, 32), (64, 64)])
with Image.open(ROOT / "favicon.ico") as icon:
    assert icon.ico.sizes() == {(16, 16), (32, 32), (64, 64)}

manifest["assets"] = [asset for asset in manifest["assets"] if asset["name"] != "favicon.ico"]
manifest["assets"].append({
    "name": "favicon.ico",
    "use": "Multi-resolution browser favicon.",
    "sizes": [16, 32, 64],
    "file": "favicon.ico",
})
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")


class GalleryLinks(HTMLParser):
    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        if tag == "img":
            assert attributes.get("alt"), "Gallery image needs alternative text."
        for key in ("href", "src"):
            value = attributes.get(key, "")
            if value and not value.startswith(("https://", "#")):
                assert (ROOT / value).is_file(), f"Missing gallery asset: {value}"


GalleryLinks().feed((ROOT / "index.html").read_text())
print("SVGs, PNG sizes, transparency, icon crop, and wordmark colors are valid.")
print("Exported lossless WebP hero assets and a multi-resolution favicon; gallery links are valid.")
