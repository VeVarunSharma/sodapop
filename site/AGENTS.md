# Sodapop website

Scope: `site/`. Apply the [repository rules](../AGENTS.md) first.

## Rules and reasons

- Keep this a private, static Astro project, independent of the Go application and
  npm distribution. Website builds must not run bundling, OAuth, or model requests.
- Use the original Soda shop identity: light surfaces, pink/purple/cyan accents,
  the supplied mascot, readable dark demo panels, and restrained carbonation.
- Keep prose canonical under root `docs/`; generate the Starlight copy from an
  explicit public-content map. Never publish the checkout or whole docs tree.
- Share one validated six-platform availability catalog between downloads and
  installation references. Homebrew remains exactly four Unix platforms.
  Pre-release is explicit; errors are not evidence of availability.
- Essential links/text work without hydration. Only small controls need React.
  Use the selected shadcn primitives and consistent Lucide SVG icons.
- Give ongoing motion a stop control and reduced-motion users static posters.
  The existing recordings are signed-out demonstrations, not live model sessions.
- Keep source artwork, generated site output, and native archives separate.
  Preserve source files and reject unknown staging/link targets.

## Start here

[Site operations](README.md), [Astro configuration](astro.config.mjs),
[brand assets](../images/brand-assets.json), and
[the distribution contract](../docs/distribution.md).

## Checks

From the repository root: `npm --prefix site run check`, `npm --prefix site test`,
`npm --prefix site run build`, and `npm --prefix site run test:e2e`.
Use an attached background process for local preview; do not leave detached servers.
