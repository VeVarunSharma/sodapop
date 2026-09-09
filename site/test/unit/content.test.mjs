import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import sharp from 'sharp';
import {
  canonicalRepository,
  installationMarker,
  prepareContent,
} from '../../scripts/prepare-content.mjs';
import { prepareAssets, publicAssets } from '../../scripts/prepare-assets.mjs';
import { repositoryRoot as canonicalRoot } from '../../scripts/site-config.mjs';

function page(source, route, order = 1) {
  return {
    source,
    route,
    title: source ? path.posix.basename(source, '.md') : 'Welcome',
    description: 'A focused public guide.',
    section: 'Guides',
    sidebar: { label: source ? path.posix.basename(source, '.md') : 'Welcome', order },
  };
}

function contentMap() {
  return {
    version: 1,
    siteRoutes: ['/', '/download/'],
    sourceLinks: { LICENSE: 'The project license.' },
    pages: [
      page(null, '/docs/'),
      page('docs/start.md', '/docs/start/', 2),
      page('docs/next.md', '/docs/next/', 3),
    ],
  };
}

async function put(root, filename, contents) {
  const target = path.join(root, filename);
  await mkdir(path.dirname(target), { recursive: true });
  await writeFile(target, contents);
}

async function tree(root, prefix = '') {
  const files = {};
  for (const entry of (await readdir(path.join(root, prefix), { withFileTypes: true }))
    .sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)) {
    const filename = path.posix.join(prefix, entry.name);
    if (entry.isDirectory()) Object.assign(files, await tree(root, filename));
    else files[filename] = await readFile(path.join(root, filename));
  }
  return files;
}

let imageFixtures;
async function fixtureImages() {
  if (imageFixtures) return imageFixtures;
  const create = { width: 3, height: 2, channels: 4, background: '#fb7185' };
  const png = await sharp({ create }).png().toBuffer();
  const gif = await sharp({ create }).gif().toBuffer();
  const webp = await sharp({ create }).webp().toBuffer();
  const svg = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="3" height="2"><rect width="3" height="2"/></svg>');
  const icon = Buffer.alloc(22);
  icon.writeUInt16LE(1, 2);
  icon.writeUInt16LE(1, 4);
  icon[6] = 3;
  icon[7] = 2;
  icon.writeUInt16LE(1, 10);
  icon.writeUInt16LE(32, 12);
  icon.writeUInt32LE(png.length, 14);
  icon.writeUInt32LE(22, 18);
  imageFixtures = { png, gif, webp, svg, ico: Buffer.concat([icon, png]) };
  return imageFixtures;
}

async function fixture(t, map = contentMap()) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'sodapop-content-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const repositoryRoot = path.join(root, 'repository');
  const siteRoot = path.join(root, 'site');
  await mkdir(repositoryRoot);
  await mkdir(siteRoot);
  await put(siteRoot, 'src/data/public-content.json', JSON.stringify(map));
  await put(repositoryRoot, 'LICENSE', 'Fixture license.\n');
  await put(repositoryRoot, 'docs/start.md', '# Start\n\n[Next](next.md#details)\n');
  await put(repositoryRoot, 'docs/next.md', '# Next\n\n## Details\n\nThe next guide.\n');
  for (const asset of publicAssets) {
    await put(repositoryRoot, asset.source, (await fixtureImages())[path.extname(asset.source).slice(1)]);
  }
  return {
    root, repositoryRoot, siteRoot, map, base: '/',
    output: path.join(siteRoot, 'src/content/docs/docs'),
  };
}

test('imports are inert and do not read publication settings or stage output', () => {
  const modules = ['prepare-content.mjs', 'prepare-assets.mjs']
    .map((filename) => new URL(`../../scripts/${filename}`, import.meta.url).href);
  const result = spawnSync(process.execPath, ['--input-type=module', '-e',
    modules.map((url) => `await import(${JSON.stringify(url)});`).join('\n')], {
    env: { ...process.env, SODAPOP_SITE_BASE: '../invalid-import-sentinel' },
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, '');
});

test('generation is deterministic, source-preserving, and limited to its owned tree', async (t) => {
  const f = await fixture(t);
  await put(f.repositoryRoot, 'docs/AGENTS.md', 'EXCLUDED GUIDANCE');
  await put(f.repositoryRoot, '.sodapop.env', 'EXCLUDED ENVIRONMENT');
  await put(f.repositoryRoot, 'docs/private/draft.md', 'EXCLUDED DRAFT');
  await put(f.siteRoot, 'src/content/docs/keep.md', 'Unrelated content collection file.');
  const original = await tree(f.repositoryRoot);
  const first = await prepareContent(f);
  const generated = await tree(f.output);

  assert.deepEqual(first.pages.map(({ route }) => route), ['/docs/', '/docs/start/', '/docs/next/']);
  assert.deepEqual(Object.keys(generated), ['index.md', 'next.md', 'start.md']);
  const start = generated['start.md'].toString();
  assert.match(start, /^---\ntitle: "start"\ndescription: /);
  assert.match(start, /sidebar:\n  label: "start"\n  order: 2\neditUrl: false\n---\n/);
  assert.doesNotMatch(start, /^# Start$/m);
  assert.match(start, /\[Next\]\(\/docs\/next\/#details\)/);
  assert.match(generated['index.md'].toString(), /\[download page\]\(\/download\/\)/);
  assert.match(generated['index.md'].toString(), /\[start\]\(\/docs\/start\/\)/);
  assert.doesNotMatch(Object.values(generated).join('\n'), /EXCLUDED/);

  await put(f.siteRoot, 'src/content/docs/docs/stale.md', 'Obsolete generated page.');
  const second = await prepareContent(f);
  assert.deepEqual(second, first);
  assert.deepEqual(await tree(f.output), generated);
  assert.deepEqual(await tree(f.repositoryRoot), original);
  assert.equal(await readFile(path.join(f.siteRoot, 'src/content/docs/keep.md'), 'utf8'),
    'Unrelated content collection file.');
  assert.deepEqual((await readdir(path.dirname(f.output))).sort(), ['docs', 'keep.md']);
});

for (const base of ['/', '/preview/', '/team/project/', '/docs/']) {
  test(`structural links, references, images, queries, and anchors use base ${base}`, async (t) => {
    const f = await fixture(t);
    const body = `# Start

[Next **guide**](next.md?view=full#details)
[Root](/download)
[Route](/docs/next/)
[Already prefixed](${base}docs/next/)
[Canonical](https://sodapop.sh/docs/next/#details)
[Reference][next]
![Still](assets/demos/overview.png "Prepared demonstration")
[![Nested](assets/demos/overview.png)](next.md)
[License](../LICENSE#terms)
[External](https://example.org/guide.md?q=x#part)
[Self](#section)
[Query](?view=all#section)

| Link | Code |
| --- | --- |
| [Next](next.md) | \`next.md\` |

\`\`\`md
[Do not rewrite](next.md)
![Or this](assets/demos/overview.png)
\`\`\`

[next]: next.md#details "A guide"
`;
    await put(f.repositoryRoot, 'docs/start.md', body);
    await prepareContent({ ...f, base });
    const output = await readFile(path.join(f.output, 'start.md'), 'utf8');
    assert.ok(output.includes(`[Next **guide**](${base}docs/next/?view=full#details)`));
    assert.ok(output.includes(`[Root](${base}download/)`));
    assert.ok(output.includes(`[Route](${base}docs/next/)`));
    assert.ok(output.includes(`[Already prefixed](${base}docs/next/)`));
    assert.ok(output.includes(`[Canonical](${base}docs/next/#details)`));
    assert.ok(output.includes(`[next]: ${base}docs/next/#details "A guide"`));
    assert.ok(output.includes(`![Still](${base}assets/demos/overview.png "Prepared demonstration")`));
    assert.ok(output.includes(`[![Nested](${base}assets/demos/overview.png)](${base}docs/next/)`));
    assert.ok(output.includes(`[License](${canonicalRepository}/blob/HEAD/LICENSE#terms)`));
    assert.ok(output.includes('[External](https://example.org/guide.md?q=x#part)'));
    assert.ok(output.includes('[Self](#section)'));
    assert.ok(output.includes(`[Query](${base}docs/start/?view=all#section)`));
    assert.ok(output.includes(`| Link | Code |\n| --- | --- |\n| [Next](${base}docs/next/) | \`next.md\` |`));
    assert.ok(output.includes('```md\n[Do not rewrite](next.md)\n![Or this](assets/demos/overview.png)\n```'));
    assert.equal(await readFile(path.join(f.repositoryRoot, 'docs/start.md'), 'utf8'), body);
  });
}

test('developer parent and child routes use a directory index without slug collisions', async (t) => {
  const map = contentMap();
  map.pages[1].route = '/docs/development/';
  map.pages[2].route = '/docs/development/architecture/';
  const f = await fixture(t, map);
  const result = await prepareContent(f);
  assert.deepEqual(result.pages.map(({ file }) => file),
    ['index.md', 'development/index.md', 'development/architecture.md']);
  assert.match(await readFile(path.join(f.output, 'development/index.md'), 'utf8'),
    /\[Next\]\(\/docs\/development\/architecture\/#details\)/);
});

test('installation catalog hook is deliberate and injected Markdown receives normal link validation', async (t) => {
  assert.equal(installationMarker, '<!-- SODAPOP_INSTALLATION_REFERENCE -->');
  const map = contentMap();
  map.pages.push(page('docs/installation.md', '/docs/installation/', 4));
  const f = await fixture(t, map);
  const source = `# Installation\n\nSee the current download page.\n\n${installationMarker}\n`;
  await put(f.repositoryRoot, 'docs/installation.md', source);
  await prepareContent(f);
  assert.ok((await readFile(path.join(f.output, 'installation.md'), 'utf8')).includes(installationMarker));

  const installationReference = [
    '## Confirmed choices', '',
    'Use [downloads](/download/). The text $& is literal.', '',
    '### Unix source', '', '```sh', 'make build', '```', '',
    '### Windows Git Bash source', '', '```sh', 'bash scripts/build.sh', '```', '',
  ].join('\n');
  await prepareContent({ ...f, base: '/preview/', installationReference });
  const generated = await readFile(path.join(f.output, 'installation.md'), 'utf8');
  assert.match(generated, /\[downloads\]\(\/preview\/download\/\)/);
  assert.ok(generated.includes('The text $& is literal.'));
  assert.ok(generated.includes('### Unix source\n\n```sh\nmake build\n```'));
  assert.ok(generated.includes('### Windows Git Bash source\n\n```sh\nbash scripts/build.sh\n```'));
  assert.ok(!generated.includes(installationMarker));
  assert.equal(await readFile(path.join(f.repositoryRoot, 'docs/installation.md'), 'utf8'), source);
  const before = await tree(f.output);
  await assert.rejects(prepareContent({ ...f, installationReference: '[Missing](/not-public/)' }),
    /Unknown public route/);
  await assert.rejects(prepareContent({ ...f, installationReference: installationMarker }),
    /without another installation catalog hook/);
  assert.deepEqual(await tree(f.output), before);
  for (const invalid of ['# Installation\n\nNo catalog marker.\n', `${source}\n${installationMarker}\n`]) {
    await put(f.repositoryRoot, 'docs/installation.md', invalid);
    await assert.rejects(prepareContent({ ...f, installationReference }), /exactly one/);
    assert.deepEqual(await tree(f.output), before);
  }
});

test('missing mapped source fails without replacing previously generated output', async (t) => {
  const f = await fixture(t);
  await prepareContent(f);
  const before = await tree(f.output);
  await rm(path.join(f.repositoryRoot, 'docs/next.md'));
  await assert.rejects(prepareContent(f), /Missing source or link target: docs\/next.md/);
  assert.deepEqual(await tree(f.output), before);
});

for (const [target, expected] of [
  ['missing.md', /Missing source or link target/],
  ['unlisted.md', /Unknown public link target/],
  ['../../outside.md', /outside the repository/],
  ['%2e%2e/%2e%2e/outside.md', /outside the repository/],
  ['../AGENTS.md', /Excluded source/],
  ['../.sodapop.env', /Excluded source/],
  ['private/draft.md', /Excluded source/],
  ['session-state/plan.md', /Excluded source/],
  ['internal-notes.md', /Excluded source/],
  ['/not-public/', /Unknown public route/],
  ['/assets/brand/not-approved.svg', /Unknown public route or asset/],
  ['../images/not-approved.svg', /Unknown public link target/],
  ['//example.org/escape', /unsafe link/],
  ['javascript:alert(1)', /Unsupported/],
  ['data:text/plain,private', /Unsupported/],
  ['file:///outside.md', /Unsupported/],
  ['https://user:password@example.org/', /credential-bearing/],
  ['https://sodapop.sh/not-public/', /Unknown public route/],
  [`${canonicalRepository}/blob/HEAD/AGENTS.md`, /Excluded source/],
  [`${canonicalRepository}/blob/HEAD/docs/unlisted.md`, /Unknown public link target/],
]) {
  test(`rejects missing, outside, excluded, or unknown target ${target}`, async (t) => {
    const f = await fixture(t);
    await put(f.repositoryRoot, 'docs/unlisted.md', 'UNLISTED PRIVATE CONTENT');
    await put(f.repositoryRoot, 'images/not-approved.svg', '<svg/>');
    await put(f.root, 'outside.md', 'OUTSIDE CONTENT');
    await put(f.repositoryRoot, 'docs/start.md', `# Start\n\n[Target](<${target}>)\n`);
    await assert.rejects(prepareContent(f), expected);
    await assert.rejects(readFile(path.join(f.output, 'start.md')), { code: 'ENOENT' });
  });
}

test('missing approved asset links fail before content is published', async (t) => {
  const f = await fixture(t);
  await put(f.repositoryRoot, 'docs/start.md', '# Start\n\n![Poster](assets/demos/overview.png)\n');
  await rm(path.join(f.repositoryRoot, 'docs/assets/demos/overview.png'));
  await assert.rejects(prepareContent(f), /Missing source or link target/);
});

test('inline and reference images cannot become arbitrary routes or remote requests', async (t) => {
  const f = await fixture(t);
  for (const source of [
    '![Not an image](next.md)',
    '![Remote image](https://example.org/tracker.png)',
    '![Reference][image]\n\n[image]: ../LICENSE',
  ]) {
    await put(f.repositoryRoot, 'docs/start.md', `# Start\n\n${source}\n`);
    await assert.rejects(prepareContent(f), /Images must reference an explicitly approved public asset/);
  }
  await put(f.repositoryRoot, 'docs/start.md',
    '# Start\n\n![Reference][poster]\n\n[poster]: assets/demos/overview.png\n');
  await prepareContent({ ...f, base: '/preview/' });
  assert.match(await readFile(path.join(f.output, 'start.md'), 'utf8'),
    /\[poster\]: \/preview\/assets\/demos\/overview\.png/);
});

test('raw HTML cannot bypass the curated Markdown links', async (t) => {
  const f = await fixture(t);
  for (const html of [
    '<a href="../AGENTS.md">Guidance</a>',
    '<img src="../.sodapop.env">',
    '<source srcset="/not-approved.png 2x">',
    '<!-- internal launch notes -->',
  ]) {
    await put(f.repositoryRoot, 'docs/start.md', `# Start\n\n${html}\n`);
    await assert.rejects(prepareContent(f), /Raw HTML is not allowed/);
  }
});

test('manifest validation fails closed for excluded sources and ambiguous routing', async (t) => {
  const f = await fixture(t);
  const cases = [
    [map => { map.pages[1].source = '../outside.md'; }, /repository-relative/],
    [map => { map.pages[1].source = 'docs/AGENTS.md'; }, /Excluded/],
    [map => { map.pages[1].source = 'docs/private/draft.md'; }, /Excluded/],
    [map => { map.pages[1].source = 'docs/internal/draft.md'; }, /Excluded/],
    [map => { map.sourceLinks['.sodapop.env'] = 'Not publishable.'; }, /Excluded/],
    [map => { map.pages[1].route = '/docs/next/'; }, /Duplicate public route/],
    [map => { map.pages[1].source = 'docs/next.md'; }, /Duplicate or ambiguous public source/],
    [map => { map.pages[1].route = '/docs/index/'; }, /routes collide/],
    [map => { map.pages[1].route = '/docs/../escape/'; }, /Invalid public documentation route/],
    [map => { map.pages[1].title = 'Two\nlines'; }, /single-line/],
    [map => { map.pages[1].sidebar.order = -1; }, /positive integer/],
    [map => { map.pages[1].draft = true; }, /Unknown public page field/],
    [map => { map.pages.shift(); }, /one generated/],
    [map => { map.siteRoutes.push('/secret/'); }, /Public site routes/],
    [map => { map.version = 2; }, /Unsupported/],
  ];
  for (const [mutate, expected] of cases) {
    const map = contentMap();
    mutate(map);
    await put(f.siteRoot, 'src/data/public-content.json', JSON.stringify(map));
    await assert.rejects(prepareContent(f), expected);
  }
});

test('symlink sources and output parents cannot read or overwrite outside files', async (t) => {
  const f = await fixture(t);
  await put(f.root, 'outside.md', 'OUTSIDE SENTINEL');
  await rm(path.join(f.repositoryRoot, 'docs/next.md'));
  await symlink(path.join(f.root, 'outside.md'), path.join(f.repositoryRoot, 'docs/next.md'));
  await assert.rejects(prepareContent(f), /outside the repository/);
  assert.equal(await readFile(path.join(f.root, 'outside.md'), 'utf8'), 'OUTSIDE SENTINEL');

  await rm(path.join(f.repositoryRoot, 'docs/next.md'));
  await put(f.repositoryRoot, 'docs/next.md', '# Next\n\nAn ordinary page.\n');
  await mkdir(path.join(f.siteRoot, 'src/content'));
  const outsideDirectory = path.join(f.root, 'outside-output');
  await mkdir(outsideDirectory);
  await put(outsideDirectory, 'keep.md', 'KEEP OUTSIDE OUTPUT');
  await symlink(outsideDirectory, path.join(f.siteRoot, 'src/content/docs'), 'dir');
  await assert.rejects(prepareContent(f), /real directories inside the site/);
  assert.deepEqual(Object.keys(await tree(outsideDirectory)), ['keep.md']);
});

test('only approved assets and lossless demo posters are staged with base-aware metadata', async (t) => {
  const f = await fixture(t);
  await put(f.repositoryRoot, 'images/fonts/Avenir.woff2', 'DO NOT COPY FONTS');
  await put(f.repositoryRoot, 'images/source/build.cjs', 'DO NOT COPY SOURCE');
  await put(f.repositoryRoot, 'images/sodapop-title.gif', 'DO NOT COPY OTHER MEDIA');
  await put(f.repositoryRoot, 'docs/assets/demos/internal.gif', 'DO NOT COPY INTERNAL MEDIA');
  await put(f.siteRoot, 'public/assets/keep.txt', 'UNRELATED PUBLIC ASSET');
  const original = await tree(f.repositoryRoot);
  const result = await prepareAssets({ ...f, base: '/preview/' });
  assert.equal(result.base, '/preview/');
  assert.equal(result.assets.length, 20);
  assert.ok(Object.isFrozen(publicAssets));
  for (const asset of result.assets) {
    assert.equal(asset.url, `/preview/${asset.destination}`);
    assert.equal(asset.width, 3);
    assert.equal(asset.height, 2);
    const copied = await readFile(path.join(f.siteRoot, 'public', asset.destination));
    assert.equal(copied.length, asset.bytes);
    assert.deepEqual(copied, original[asset.source]);
  }
  assert.equal(result.posters.length, 4);
  for (const poster of result.posters) {
    assert.equal(poster.url, `/preview/${poster.destination}`);
    assert.equal(poster.width, 3);
    assert.equal(poster.height, 2);
    assert.equal(poster.format, 'webp');
    assert.equal((await readFile(path.join(f.siteRoot, 'public', poster.destination))).length, poster.bytes);
  }
  const generated = await tree(path.join(f.siteRoot, 'public/assets'));
  assert.deepEqual(Object.keys(generated).sort(),
    [
      ...publicAssets.map(({ destination }) => destination.slice('assets/'.length)),
      ...result.posters.map(({ destination }) => destination.slice('assets/'.length)),
      'keep.txt',
    ].sort());
  assert.doesNotMatch(Object.values(generated).join('\n'), /DO NOT COPY/);
  await put(f.siteRoot, 'public/assets/brand/stale.svg', 'OLD GENERATED ARTWORK');
  assert.deepEqual(await prepareAssets({ ...f, base: '/preview/' }), result);
  assert.deepEqual(await tree(path.join(f.siteRoot, 'public/assets')), generated);
  assert.deepEqual(await tree(f.repositoryRoot), original);
});

test('missing or corrupt assets fail before replacing the previous asset set', async (t) => {
  const f = await fixture(t);
  await prepareAssets(f);
  const before = await tree(path.join(f.siteRoot, 'public/assets'));
  await rm(path.join(f.repositoryRoot, 'docs/assets/demos/diff.png'));
  await assert.rejects(prepareAssets(f), /Missing source or link target/);
  assert.deepEqual(await tree(path.join(f.siteRoot, 'public/assets')), before);
  await put(f.repositoryRoot, 'docs/assets/demos/diff.png', 'This is not an image');
  await assert.rejects(prepareAssets(f), /unsupported image format/i);
  assert.deepEqual(await tree(path.join(f.siteRoot, 'public/assets')), before);
  await put(f.repositoryRoot, 'docs/assets/demos/diff.png', (await fixtureImages()).png);
  await put(f.repositoryRoot, 'images/favicon.ico', Buffer.from([0, 0, 1, 0, 1, 0]));
  await assert.rejects(prepareAssets(f), /Invalid ICO image directory/);
  assert.deepEqual(await tree(path.join(f.siteRoot, 'public/assets')), before);
});

test('assets reject symlink inputs and symlink generated directories', async (t) => {
  const f = await fixture(t);
  await put(f.root, 'outside.png', (await fixtureImages()).png);
  await rm(path.join(f.repositoryRoot, 'images/sodapop-og.png'));
  await symlink(path.join(f.root, 'outside.png'), path.join(f.repositoryRoot, 'images/sodapop-og.png'));
  await assert.rejects(prepareAssets(f), /outside the repository/);
  await rm(path.join(f.repositoryRoot, 'images/sodapop-og.png'));
  await put(f.repositoryRoot, 'images/sodapop-og.png', (await fixtureImages()).png);
  await mkdir(path.join(f.siteRoot, 'public/assets'), { recursive: true });
  const outside = path.join(f.root, 'outside-assets');
  await mkdir(outside);
  await put(outside, 'keep.txt', 'KEEP');
  await symlink(outside, path.join(f.siteRoot, 'public/assets/brand'), 'dir');
  await assert.rejects(prepareAssets(f), /real directory/);
  assert.deepEqual(Object.keys(await tree(outside)), ['keep.txt']);
});

test('the real curated prose generates all approved routes using isolated fixture roots', async (t) => {
  const map = JSON.parse(await readFile(path.join(canonicalRoot, 'site/src/data/public-content.json'), 'utf8'));
  const f = await fixture(t, map);
  for (const source of [...map.pages.map((entry) => entry.source).filter(Boolean), ...Object.keys(map.sourceLinks)]) {
    await put(f.repositoryRoot, source, await readFile(path.join(canonicalRoot, source)));
  }
  const sourceBefore = await tree(f.repositoryRoot);
  const result = await prepareContent({ ...f, base: '/sodapop/' });
  assert.equal(result.pages.length, 11);
  assert.equal(new Set(result.pages.map(({ file }) => file)).size, 11);
  assert.equal(result.pages.some(({ source }) => source === 'docs/brand-voice.md'), false);
  const generated = await tree(f.output);
  const hub = generated['index.md'].toString();
  assert.match(hub, /^title: "Sodapop docs"$/m);
  assert.match(hub, /First sip\? Let's get Sodapop running in your project\./);
  assert.match(hub, /\[download page\]\(\/sodapop\/download\/\)/);
  const customization = generated['customization.md'].toString();
  assert.match(customization, /^title: "Customization"$/m);
  assert.match(customization, /How fizzy are we feeling\? Open `\/theme`/);
  assert.match(customization, /`\/theme arcade`/);
  assert.match(customization.replace(/\s+/g, ' '),
    /Personality changes UI copy and decoration, not the model's capabilities, Copilot access, or tool-approval policy/);
  assert.match(customization.replace(/\s+/g, ' '),
    /Color, motion, personality, and the character set are independent choices/);
  const commands = generated['commands.md'].toString();
  assert.match(commands, /The good stuff starts with `\/`\./);
  assert.match(commands, /^## Built-in workflows$/m);
  assert.match(commands, /^## Conversation, model, and change controls$/m);
  assert.match(commands, /^## Connect and extend$/m);
  assert.match(commands, /Ctrl\+J \/ Shift\+Enter \/ Alt\+Enter/);
  assert.match(commands, /Too many ideas\? Excellent\./);
  assert.match(generated['installation.md'].toString(), /\[download page\]\(\/sodapop\/download\/\)/);
  assert.doesNotMatch(generated['installation.md'].toString(), /\b(?:npm install|brew install)\b/);
  const installation = generated['installation.md'].toString().replace(/\s+/g, ' ');
  assert.match(installation, /Homebrew is not currently published/);
  assert.match(installation, /Windows x64 npm support is conditional/);
  assert.match(installation, /qualified Windows ZIP and the matching npm package version is actually published/);
  assert.match(installation, /Matching hashes do not verify release attestations/);
  const troubleshooting = generated['troubleshooting.md'].toString().replace(/\s+/g, ' ');
  assert.match(troubleshooting, /@sodapop-sh\/windows-amd64` package version being published/);
  assert.match(troubleshooting, /Launcher and native package versions must match/);
  assert.match(generated['getting-started.md'].toString(), /end users do not need to register an OAuth app/);
  assert.match(generated['development/authentication.md'].toString(),
    /\/sodapop\/assets\/brand\/sodapop-oauth\.png/);
  assert.match(generated['development/index.md'].toString(), /\/docs\/development\/authentication\//);
  assert.ok(generated['development/index.md'].toString()
    .includes(`[npm packaging guide](${canonicalRepository}/blob/HEAD/npm/README.md)`));
  const development = generated['development/index.md'].toString().replace(/\s+/g, ' ');
  assert.match(development, /development template, not the published support matrix/);
  assert.match(development, /select an exact stable or prerelease tag/);
  assert.match(development, /prerelease npm uses `preview`, while Homebrew remains stable-only/);
  const distribution = generated['development/distribution.md'].toString().replace(/\s+/g, ' ');
  for (const field of ['copilot_sdk_version', 'copilot_runtime_version', 'archive_sha256', 'binary_sha256']) {
    assert.ok(distribution.includes(`\`${field}\``), field);
  }
  assert.match(distribution, /declared subset for local tests/);
  assert.match(distribution, /must not be used to hide a failed platform that a public channel still advertises/);
  assert.match(distribution, /Windows portable packages use ZIP/);
  assert.match(distribution, /not by itself proof of publisher identity/);
  assert.deepEqual(await tree(f.repositoryRoot), sourceBefore);
});

test('the original approved artwork and recordings retain their bytes and dimensions', async (t) => {
  const f = await fixture(t);
  const originals = new Map();
  for (const { source } of publicAssets) {
    const bytes = await readFile(path.join(canonicalRoot, source));
    originals.set(source, bytes);
    await put(f.repositoryRoot, source, bytes);
  }
  const { assets, posters } = await prepareAssets({ ...f, base: '/sodapop/' });
  const bySource = new Map(assets.map((asset) => [asset.source, asset]));
  for (const [source, [width, height]] of Object.entries({
    'images/sodapop-mascot.webp': [1200, 1200],
    'images/sodapop-wordmark.svg': [1200, 340],
    'images/sodapop-lockup-on-light.svg': [1280, 320],
    'images/sodapop-lockup-on-dark.svg': [1280, 320],
    'images/sodapop-og.png': [1200, 630],
    'images/favicon.ico': [64, 64],
  })) {
    assert.equal(bySource.get(source).width, width, source);
    assert.equal(bySource.get(source).height, height, source);
  }
  for (const name of ['overview', 'commands', 'themes', 'diff']) {
    const animation = bySource.get(`docs/assets/demos/${name}.gif`);
    const poster = bySource.get(`docs/assets/demos/${name}.png`);
    assert.equal(animation.width, poster.width, name);
    assert.equal(animation.height, poster.height, name);
  }
  for (const asset of assets) {
    assert.deepEqual(await readFile(path.join(f.siteRoot, 'public', asset.destination)), originals.get(asset.source));
    assert.deepEqual(await readFile(path.join(canonicalRoot, asset.source)), originals.get(asset.source));
  }
  assert.equal(posters.length, 4);
  for (const poster of posters) {
    const original = bySource.get(poster.source);
    assert.equal(poster.format, 'webp');
    assert.equal(poster.width, original.width);
    assert.equal(poster.height, original.height);
    assert.ok(poster.bytes < original.bytes, poster.destination);
    const metadata = await sharp(path.join(f.siteRoot, 'public', poster.destination)).metadata();
    assert.equal(metadata.format, 'webp');
    assert.equal(metadata.width, original.width);
    assert.equal(metadata.height, original.height);
  }
});
