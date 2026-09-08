# Animated Sodapop title

The README's animated title is original Sodapop artwork. It takes the compact,
playful presentation of Crush's title GIF as inspiration without using Crush's
logo, lettering, or artwork.

The animation reuses `images/sodapop-wordmark.png` and
`images/sodapop-mascot.png`, preserving their proportions, rounded lettering,
pink-to-purple gradient, silver pull tab, cyan details, and friendly expression.
The surrounding colors come from `images/brand-assets.json`. Existing static
assets, application branding, and OAuth configuration are not changed.

## Regenerate

From a source checkout, use Python 3.10+ and the same Pillow dependency as the brand
asset tooling:

```sh
python3 -m pip install -r images/source/requirements.txt
make brand
```

`make brand` runs the renderer's focused tests and generates:

| Asset | Purpose |
|---|---|
| `images/sodapop-title.gif` | 1000 x 560, 4-second, 20 fps looping title |
| `images/sodapop-title.png` | Full-color still and reduced-motion alternative |

It also refreshes the title's entry in `images/brand-assets.json`. Neither source
PNG is rewritten. No external fonts are needed: the main lettering is already
outlined in the supplied artwork, and the small caption uses Pillow's embedded
font. The renderer uses no network, credentials, application runtime, or terminal.

To export a candidate without changing the published files or source catalog:

```sh
python3 -B images/source/animate.py --output /tmp/sodapop-title-preview
```

If regenerating the whole static brand set, run `images/source/build.cjs` with
its required licensed font inputs, then `images/source/finish.py`, and finally
`make brand`. The last step registers the animated asset in the rebuilt catalog.

## Motion and delivery

The wordmark stays visible while a soft sheen passes over it. The mascot gently
floats, carbonation rises, and small lights follow the orbital accents. Positions
are periodic and deterministic: the end transitions back into the start without
a cut, blank frame, or full-screen flash.

A shared GIF palette and transparent frame deltas keep colors stable and the file
small. Exports larger than 2 MiB fail before replacing existing artwork. Files are
rendered and decoded in temporary staging before publication, with temporary
files cleaned on success and failure.

The README displays the title at 640 px wide. Its image links to the PNG still,
and its `picture` element requests that still for reduced-motion preferences.
The full-color PNG remains available even on renderers that ignore that media
query. The existing VHS feature demos remain separate and unchanged.

The normal Go suite checks the shipped media, README placement, and packaging.
Renderer tests can also run directly:

```sh
python3 -B -m unittest discover -s images/source -p 'test_animate.py'
```
