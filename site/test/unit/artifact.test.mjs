import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { checkArtifact } from '../../scripts/check-artifact.mjs';

async function fixture(t, base = '/') {
  const root = await mkdtemp(path.join(os.tmpdir(), 'sodapop-site-artifact-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  async function write(name, content) {
    await mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await writeFile(path.join(root, name), content);
  }
  const html = (link = '') => `<html><head><meta name="sodapop-build" content="public"></head><body><h1 id="hello">Sodapop</h1>${link}</body></html>`;
  await write('index.html', html(`<a href="${base}docs/#hello">Docs</a><img src="${base}assets/brand/mascot.svg">`));
  for (const page of ['download/index.html', 'docs/index.html', '404.html']) await write(page, html());
  await write('robots.txt', 'User-agent: *');
  await write('assets/brand/mascot.svg', '<svg/>');
  await write('pagefind/pagefind.js', '// search fixture');
  return { root, write, html, publication: { base, origin: 'https://sodapop.sh' } };
}

test('accepts complete static artifacts at root and repository bases', async (t) => {
  for (const base of ['/', '/sodapop/']) {
    const options = await fixture(t, base);
    assert.equal((await checkArtifact({ ...options, requirePublic: true })).pages, 4);
  }
});

test('rejects missing targets, anchors, and escaping base links', async (t) => {
  for (const link of ['/sodapop/missing/', '/sodapop/docs/#missing', '/docs/', 'javascript:alert(1)']) {
    const options = await fixture(t, '/sodapop/');
    await options.write('index.html', options.html(`<a href="${link}">Invalid</a>`));
    await assert.rejects(checkArtifact(options), /Broken local|escapes|Unsafe URL/);
  }
});

test('rejects repository leakage, missing search, and preview deployment', async (t) => {
  const leaked = await fixture(t);
  await leaked.write('assets/.env', 'PRIVATE_SENTINEL');
  await assert.rejects(checkArtifact(leaked), /Non-public/);
  const searchless = await fixture(t);
  await rm(path.join(searchless.root, 'pagefind/pagefind.js'));
  await assert.rejects(checkArtifact(searchless), /search index/);
  const preview = await fixture(t);
  await preview.write('index.html', preview.html().replace('content="public"', 'content="preview"'));
  assert.equal((await checkArtifact(preview)).pages, 4);
  await assert.rejects(checkArtifact({ ...preview, requirePublic: true }), /preview HTML/);
});
