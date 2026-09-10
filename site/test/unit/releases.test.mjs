import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { readCatalog, catalogPaths } from '../../scripts/release-catalog.mjs';
import { resolveReleases } from '../../scripts/resolve-releases.mjs';
import { api, tapAPI, registry, version, platforms, releaseFixture } from '../fixtures/releases.mjs';

const unavailable = { status: 'unpublished', command: null, version: null, url: null, platforms: [] };
const preRelease = {
  schemaVersion: 1, mode: 'pre-release', version: null, releaseUrl: null, manifestUrl: null,
  commit: null, copilotSdkVersion: null, copilotRuntimeVersion: null, artifacts: [],
  channels: { npm: unavailable, homebrew: unavailable },
};
const previewEnvironment = { SODAPOP_SITE_PREVIEW: '1' };
const subprocessGuard = new URL('../fixtures/release-no-network.mjs', import.meta.url).href;
const manifestURL = `https://github.com/VeVarunSharma/sodapop/releases/download/v${version}/sodapop-${version}-manifest.json`;
const snapshot = async (f) => JSON.parse(await readFile(f.resolvedPath, 'utf8'));
const missing = (f, url, kind = 'npm') => f.setJSON(url, kind === 'npm' ? { error: 'Not found' } : { message: 'Not Found' }, 404);

test('configured pre-release is explicit, serializable, offline, and contains no fabricated availability', async (t) => {
  const f = await releaseFixture(t, { mode: 'pre-release' });
  const catalog = await readCatalog(f.readOptions);
  assert.deepEqual(catalog, preRelease);
  assert.deepEqual(JSON.parse(JSON.stringify(catalog)), catalog);
});

test('explicit previews bypass missing, published, and invalid release snapshots without network or writes', async (t) => {
  for (const state of ['missing', 'published', 'invalid']) {
    await t.test(state, async (t) => {
      const f = await releaseFixture(t, { npm: true, homebrew: true });
      let previous;
      if (state === 'published') {
        const published = await resolveReleases(f.options);
        assert.equal(published.channels.npm.status, 'published');
        assert.equal(published.channels.homebrew.status, 'published');
        previous = await readFile(f.resolvedPath, 'utf8');
      } else if (state === 'invalid') {
        await mkdir(path.dirname(f.resolvedPath));
        previous = '{"malformed":';
        await writeFile(f.resolvedPath, previous);
      }
      f.requests.length = 0;
      let requests = 0;
      const reader = { ...f.readOptions, environment: previewEnvironment };
      const resolver = {
        ...f.options, environment: previewEnvironment,
        fetch: async () => { requests += 1; throw new Error('Preview must not fetch'); },
        replaceFile: async () => { assert.fail('Preview must not replace metadata'); },
      };
      assert.deepEqual(await readCatalog(reader), { ...preRelease, preview: true });
      assert.deepEqual(await resolveReleases(resolver), { ...preRelease, preview: true });
      assert.equal(requests, 0);
      assert.equal(f.requests.length, 0);
      if (state === 'missing') {
        await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
        await assert.rejects(readdir(path.dirname(f.resolvedPath)), { code: 'ENOENT' });
      } else {
        assert.equal(await readFile(f.resolvedPath, 'utf8'), previous);
        assert.deepEqual(await readdir(path.dirname(f.resolvedPath)), ['releases.json']);
      }
      for (const environment of [{}, { SODAPOP_SITE_PREVIEW: '0' }]) {
        if (state === 'published') {
          const catalog = await readCatalog({ ...f.readOptions, environment });
          assert.equal(catalog.mode, 'release');
          assert.equal(catalog.preview, undefined);
        } else {
          await assert.rejects(readCatalog({ ...f.readOptions, environment }),
            state === 'missing' ? /requires local/ : /valid JSON/);
        }
      }
    });
  }
});

test('preview flags accept only explicit string 0 or 1, with absence defaulting to production', async (t) => {
  for (const value of ['', 'true', 'false', 'yes', '01', '2', ' 1', '1 ', '1\n', true, false, 0, 1, null]) {
    await t.test(JSON.stringify(value), async (t) => {
      const f = await releaseFixture(t, { mode: 'pre-release' });
      const environment = { SODAPOP_SITE_PREVIEW: value };
      await assert.rejects(readCatalog({ ...f.readOptions, environment }), /SODAPOP_SITE_PREVIEW must be 0 or 1/);
      await assert.rejects(resolveReleases({ ...f.options, environment }), /SODAPOP_SITE_PREVIEW must be 0 or 1/);
      assert.equal(f.requests.length, 0);
    });
  }
  const f = await releaseFixture(t, { mode: 'pre-release' });
  for (const environment of [{}, { SODAPOP_SITE_PREVIEW: '0' }]) {
    assert.deepEqual(await readCatalog({ ...f.readOptions, environment }), preRelease);
    assert.deepEqual(await resolveReleases({ ...f.options, environment }), preRelease);
  }
});

test('production defaults never turn lookup failures into preview catalogs', async (t) => {
  const f = await releaseFixture(t);
  f.setJSON(api, { message: 'Forbidden' }, 403);
  for (const environment of [{}, { SODAPOP_SITE_PREVIEW: '0' }]) {
    await assert.rejects(readCatalog({ ...f.readOptions, environment }), /requires local/);
    await assert.rejects(resolveReleases({ ...f.options, environment }), /HTTP 403/);
    await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
  }
  assert.equal(f.requests.length, 2);
});

test('default reader and resolver honor the shared process preview flag without injected catalog paths', () => {
  const reader = new URL('../../scripts/release-catalog.mjs', import.meta.url).href;
  const resolver = new URL('../../scripts/resolve-releases.mjs', import.meta.url).href;
  const code = `
    import { readCatalog } from ${JSON.stringify(reader)};
    import { resolveReleases } from ${JSON.stringify(resolver)};
    console.log(JSON.stringify([await readCatalog(), await resolveReleases()]));
  `;
  const result = execFileSync(process.execPath, ['--import', subprocessGuard, '--input-type=module', '-e', code], {
    encoding: 'utf8', env: { ...process.env, ...previewEnvironment },
  });
  assert.deepEqual(JSON.parse(result), [{ ...preRelease, preview: true }, { ...preRelease, preview: true }]);
});

test('pre-release sync never fetches, creates output, or overwrites an old snapshot', async (t) => {
  const f = await releaseFixture(t, { mode: 'pre-release' });
  const result = await resolveReleases(f.options);
  assert.equal(result.mode, 'pre-release');
  assert.equal(f.requests.length, 0);
  await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
  await mkdir(path.dirname(f.resolvedPath));
  await writeFile(f.resolvedPath, 'older snapshot is deliberately ignored');
  assert.equal((await resolveReleases(f.options)).version, null);
  assert.equal(await readFile(f.resolvedPath, 'utf8'), 'older snapshot is deliberately ignored');
  assert.equal(f.requests.length, 0);
});

test('release-mode builds require local metadata rather than silently consulting GitHub', async (t) => {
  const f = await releaseFixture(t);
  await assert.rejects(readCatalog(f.readOptions), /requires local .*releases\.json.*release:sync/);
  assert.equal(f.requests.length, 0);
});

test('actual snake_case native manifest resolves all five targets without archive downloads', async (t) => {
  const f = await releaseFixture(t);
  const catalog = await resolveReleases(f.options);
  assert.equal(catalog.mode, 'release');
  assert.equal(catalog.version, version);
  assert.equal(catalog.manifestUrl, manifestURL);
  assert.equal(catalog.commit, f.manifest.commit);
  assert.equal(catalog.copilotRuntimeVersion, f.manifest.copilot_runtime_version);
  assert.equal(catalog.copilotSdkVersion, f.manifest.copilot_sdk_version);
  assert.deepEqual(catalog.artifacts.map(({ platform }) => platform), platforms);
  assert.equal(catalog.artifacts[4].binary, 'sodapop.exe');
  assert.equal(catalog.artifacts[4].format, 'zip');
  for (const [index, artifact] of catalog.artifacts.entries()) {
    assert.equal(artifact.archive, f.manifest.artifacts[index].archive);
    assert.equal(artifact.archiveSha256, f.manifest.artifacts[index].archive_sha256);
    assert.equal(artifact.binarySha256, f.manifest.artifacts[index].binary_sha256);
    assert.equal(artifact.checksumUrl, `${artifact.archiveUrl}.sha256`);
    assert.equal(f.requests.some(({ url }) => url === artifact.archiveUrl), false);
  }
  assert.equal(f.requests.length, 9);
  assert.deepEqual(catalog.channels, { npm: unavailable, homebrew: unavailable });
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
  const data = await snapshot(f);
  assert.equal(data.source, 'fixture');
  assert.deepEqual(data.catalog, catalog);
  assert.equal(f.requests.some(({ url }) => url.startsWith(registry) || url.startsWith(tapAPI)), false);
});

test('documented SDK field and Windows ZIP remain explicit, with deterministic platform ordering', async (t) => {
  const f = await releaseFixture(t, { windowsZip: true, sdk: true });
  f.manifest.artifacts.reverse();
  f.refreshManifest();
  const catalog = await resolveReleases(f.options);
  assert.deepEqual(catalog.artifacts.map(({ platform }) => platform), platforms);
  assert.equal(catalog.copilotSdkVersion, '1.0.13');
  assert.equal(catalog.artifacts[4].archive, `sodapop-${version}-windows-amd64.zip`);
  assert.equal(catalog.artifacts[4].format, 'zip');
  assert.equal(catalog.artifacts[4].binary, 'sodapop.exe');
});

test('exact configured stable tags use the tagged release endpoint, never latest/download', async (t) => {
  const f = await releaseFixture(t);
  f.configuration.releaseTag = `v${version}`;
  await f.saveConfiguration();
  await resolveReleases(f.options);
  assert.ok(f.requests.some(({ url }) => url === `${api}/releases/tags/v${version}`));
  assert.equal(f.requests.some(({ url }) => url.includes('/latest')), false);
});

test('source/config identities and explicit opt-ins fail before any public requests', async (t) => {
  const cases = [
    ['unknown mode', (c) => { c.mode = 'auto'; }],
    ['schema', (c) => { c.schemaVersion = 2; }],
    ['repository', (c) => { c.repository = 'somebody/sodapop'; }],
    ['host instead of identity', (c) => { c.repository = 'https://github.com/VeVarunSharma/sodapop'; }],
    ['package', (c) => { c.channels.npm.package = '@different/cli'; }],
    ['nonboolean opt-in', (c) => { c.channels.npm.verify = 'true'; }],
    ['tap ownership missing', (c) => { c.channels.homebrew.verify = true; }],
    ['wrong tap', (c) => { c.channels.homebrew.repository = 'other/homebrew-sodapop'; }],
    ['wrong formula', (c) => { c.channels.homebrew.formula = 'different'; }],
    ['unscoped credentials', (c) => { c.token = 'do-not-serialize'; }],
    ['fixture flag', (c) => { c.allowFixtures = true; }],
    ['missing channel', (c) => { delete c.channels.npm; }],
    ...['latest', '1.2.3', 'v0.0.0', 'v01.2.3', 'v1.2.3-01', 'v1.2.3+dev', 'v1.2.3-', 'v1.2.3\n', 'v1.2.3/else']
      .map((tag) => [`invalid tag ${JSON.stringify(tag)}`, (c) => { c.releaseTag = tag; }]),
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t);
      mutate(f.configuration);
      await f.saveConfiguration();
      await assert.rejects(resolveReleases(f.options));
      assert.equal(f.requests.length, 0);
      await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
    });
  }
});

test('malformed native schema, platform, version, filename, and hashes cannot produce a catalog', async (t) => {
  const cases = [
    ['wrong schema', (m) => { m.schema_version = 2; }],
    ['camel-case schema', (m) => { m.schemaVersion = m.schema_version; delete m.schema_version; }],
    ['extra metadata', (m) => { m.token = 'must-not-be-persisted'; }],
    ['missing runtime', (m) => { delete m.copilot_runtime_version; }],
    ['missing SDK', (m) => { delete m.copilot_sdk_version; }],
    ['bad runtime', (m) => { m.copilot_runtime_version = 'development'; }],
    ['newline runtime', (m) => { m.copilot_runtime_version += '\n'; }],
    ['invalid SDK', (m) => { m.copilot_sdk_version = null; }],
    ['newline SDK', (m) => { m.copilot_sdk_version = 'v1.2.3\n'; }],
    ['short commit', (m) => { m.commit = 'abc123'; }],
    ['zero commit', (m) => { m.commit = '0'.repeat(40); }],
    ['newline commit', (m) => { m.commit += '\n'; }],
    ['release version mismatch', (m) => { m.version = '1.2.4'; }],
    ...['dev', '0.0.0', 'v1.2.3', '01.2.3', '1.2.3-rc.1', '1.2.3+local', '1.2.3\n', '1.2.3/other']
      .map((version) => [`version ${JSON.stringify(version)}`, (m) => { m.version = version; }]),
    ['missing native target', (m) => { m.artifacts.pop(); }],
    ['duplicate target', (m) => { m.artifacts[4] = { ...m.artifacts[0] }; }],
    ['unsupported Windows arm64', (m) => { m.artifacts[4].platform = 'windows/arm64'; }],
    ['extra native target', (m) => { m.artifacts.push({ ...m.artifacts[0], platform: 'freebsd/amd64' }); }],
    ['artifact object', (m) => { m.artifacts = {}; }],
    ['artifact null', (m) => { m.artifacts[0] = null; }],
    ['unknown artifact key', (m) => { m.artifacts[0].url = 'https://example.test/a'; }],
    ['wrong binary key', (m) => { m.artifacts[0].binarySha256 = m.artifacts[0].binary_sha256; delete m.artifacts[0].binary_sha256; }],
    ['archive traversal', (m) => { m.artifacts[0].archive = `../${m.artifacts[0].archive}`; }],
    ['absolute archive URL', (m) => { m.artifacts[0].archive = `https://github.com/${m.artifacts[0].archive}`; }],
    ['wrong filename version', (m) => { m.artifacts[0].archive = m.artifacts[0].archive.replace(version, '1.2.4'); }],
    ['wrong filename platform', (m) => { m.artifacts[0].archive = m.artifacts[1].archive; }],
    ['Unix ZIP', (m) => { m.artifacts[0].archive = m.artifacts[0].archive.replace('.tar.gz', '.zip'); }],
    ['Windows unexpected format', (m) => { m.artifacts[4].archive = m.artifacts[4].archive.replace('.zip', '.exe'); }],
    ['Windows tarball', (m) => { m.artifacts[4].archive = m.artifacts[4].archive.replace('.zip', '.tar.gz'); }],
    ['uppercase hash', (m) => { m.artifacts[0].archive_sha256 = 'A'.repeat(64); }],
    ['short hash', (m) => { m.artifacts[0].binary_sha256 = 'f'.repeat(63); }],
    ['newline hash', (m) => { m.artifacts[0].binary_sha256 += '\n'; }],
    ['non-string hash', (m) => { m.artifacts[0].binary_sha256 = 42; }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t);
      mutate(f.manifest);
      f.refreshManifest();
      await assert.rejects(resolveReleases(f.options), /[Mm]anifest/);
      await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
    });
  }
});

test('native manifest parsing rejects duplicate/escaped keys and excessive JSON nesting', async (t) => {
  for (const transform of [
    (text) => text.replace('"schema_version":1', '"schema_version":1,"schema_version":1'),
    (text) => text.replace('"schema_version":1', '"schema_version":1,"schema_\\u0076ersion":1'),
    (text) => text.replace('"platform":"darwin/arm64"', '"platform":"darwin/arm64","platform":"darwin/arm64"'),
    (text) => text.replace('"schema_version":1', `"extra":${'['.repeat(33)}0${']'.repeat(33)},"schema_version":1`),
  ]) {
    await t.test(transform.toString(), async (t) => {
      const f = await releaseFixture(t);
      f.setManifestText(transform(JSON.stringify(f.manifest)));
      await assert.rejects(resolveReleases(f.options), /duplicate JSON|nesting limit/);
    });
  }
});

test('public release/repository metadata must identify the exact selected non-draft publication', async (t) => {
  const cases = [
    ['draft', (f) => { f.release.draft = true; }],
    ['prerelease', (f) => { f.release.prerelease = true; }],
    ['prerelease tag', (f) => { f.release.tag_name = 'v1.2.3-rc.1'; }],
    ['unpublished', (f) => { f.release.published_at = null; }],
    ['non-ISO publication', (f) => { f.release.published_at = '2026'; }],
    ['invalid publication date', (f) => { f.release.published_at = '2026-02-30T12:00:00Z'; }],
    ['newline publication date', (f) => { f.release.published_at += '\n'; }],
    ['wrong release host', (f) => { f.release.html_url = 'https://example.test/releases/tag/v1.2.3'; }],
    ['wrong release API', (f) => { f.release.url = f.release.url.replace('/123', '/124'); }],
    ['wrong assets API', (f) => { f.release.assets_url = 'https://example.test/assets'; }],
    ['private repository', (f) => { f.repository.private = true; }],
    ['moved repository', (f) => { f.repository.full_name = 'other/sodapop'; }],
    ['repository URL', (f) => { f.repository.html_url += '/different'; }],
    ['repository API', (f) => { f.repository.url = 'https://example.test'; }],
    ['partial release assets', (f) => { f.release.assets.pop(); }],
    ['extra release assets', (f) => { f.release.assets.push({ ...f.release.assets[0] }); }],
    ['duplicate assets', (f) => { f.release.assets[1] = { ...f.release.assets[0] }; }],
    ['duplicate asset ids', (f) => { f.release.assets[1].id = f.release.assets[0].id; }],
    ['not uploaded', (f) => { f.release.assets[0].state = 'new'; }],
    ['empty asset', (f) => { f.release.assets[0].size = 0; }],
    ['implausible asset size', (f) => { f.release.assets[0].size = Number.MAX_SAFE_INTEGER; }],
    ['asset URL', (f) => { f.release.assets[0].url = 'https://example.test/1'; }],
    ['asset digest syntax', (f) => { f.release.assets[0].digest = 'sha512:abc'; }],
    ['asset digest mismatch', (f) => { f.release.assets[0].digest = `sha256:${'f'.repeat(64)}`; }],
    ['missing manifest asset', (f) => {
      const a = f.release.assets.at(-1);
      a.name = 'other-manifest.json';
      a.browser_download_url = a.browser_download_url.replace(f.manifestName, a.name);
    }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t);
      mutate(f);
      await assert.rejects(resolveReleases(f.options));
    });
  }
});

test('download URLs reject wrong hosts, aliases, credentials, query parameters, and encoding tricks', async (t) => {
  for (const mutate of [
    (url) => url.replace('github.com', 'github.com.example.test'),
    (url) => url.replace('https:', 'http:'),
    (url) => url.replace('github.com', 'github.com:444'),
    (url) => url.replace('github.com', 'user:secret@github.com'),
    (url) => `${url}?token=secret`,
    (url) => `${url}#fragment`,
    (url) => url.replace(`/download/v${version}/`, '/latest/download/'),
    (url) => url.replace('/sodapop-', '/%73odapop-'),
    (url) => `${url}\n`,
    (url) => url.replace('/VeVarunSharma/', '/other/'),
  ]) {
    await t.test(mutate.toString(), async (t) => {
      const f = await releaseFixture(t);
      f.release.assets[0].browser_download_url = mutate(f.release.assets[0].browser_download_url);
      await assert.rejects(resolveReleases(f.options), /metadata\/URLs/);
      assert.equal(f.requests.length, 2);
    });
  }
});

test('sidecars bind the exact manifest digest and basename and contain only one entry', async (t) => {
  for (const [name, transform] of [
    ['wrong hash', (_, a) => `${'0'.repeat(64)}  ${a.archive}\n`],
    ['wrong basename', (_, a) => `${a.archive_sha256}  wrong.tar.gz\n`],
    ['extra token', (line) => `${line.trimEnd()} extra\n`],
    ['two entries', (line) => `${line}${line}`],
    ['blank extra line', (line) => `${line}\n`],
    ['leading whitespace', (line) => ` ${line}`],
    ['trailing whitespace', (line) => `${line.trimEnd()} \n`],
    ['empty sidecar', () => ''],
  ]) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t);
      const artifact = f.manifest.artifacts[0];
      f.setChecksum(artifact.platform, transform(`${artifact.archive_sha256}  ${artifact.archive}\n`, artifact));
      await assert.rejects(resolveReleases(f.options), /checksum|asset metadata/);
    });
  }
});

test('GitHub optional asset digests are cross-checked when supplied, not fabricated when absent', async (t) => {
  const f = await releaseFixture(t);
  for (const asset of f.release.assets) delete asset.digest;
  assert.equal((await resolveReleases(f.options)).artifacts.length, 5);
  f.release.assets.at(-1).digest = `sha256:${'f'.repeat(64)}`;
  await assert.rejects(resolveReleases(f.options), /manifest bytes.*size\/digest/);
});

test('lightweight and annotated release tags must bind the manifest commit', async (t) => {
  const f = await releaseFixture(t);
  const sha = createHash('sha1').update('annotated fixture tag').digest('hex');
  const commit = { ...f.gitRef.object };
  f.gitRef.object = { type: 'tag', sha, url: `${api}/git/tags/${sha}` };
  const tag = { sha, object: commit };
  f.setJSON(`${api}/git/tags/${sha}`, tag);
  assert.equal((await resolveReleases(f.options)).commit, commit.sha);
  tag.object = { ...f.gitRef.object };
  await assert.rejects(resolveReleases(f.options), /cyclic/);
  f.gitRef.object = { ...commit, sha: 'f'.repeat(40), url: `${api}/git/commits/${'f'.repeat(40)}` };
  await assert.rejects(resolveReleases(f.options), /manifest commit does not match/);
  f.gitRef.object.url = 'https://example.test/tag';
  await assert.rejects(resolveReleases(f.options), /unsafe target URL/);
});

test('a four-Unix npm manifest exposes only its declared packages, not every native download', async (t) => {
  const f = await releaseFixture(t, { npm: true });
  const catalog = await resolveReleases(f.options);
  assert.deepEqual(catalog.channels.npm, {
    status: 'published', command: 'npm install --global @sodapop-sh/cli', version,
    url: 'https://www.npmjs.com/package/@sodapop-sh/cli', platforms: platforms.slice(0, 4),
  });
  assert.deepEqual(catalog.channels.homebrew, unavailable);
  const requests = f.requests.filter(({ url }) => url.startsWith(registry));
  assert.equal(requests.length, 6);
  assert.equal(requests.some(({ url }) => /windows|\.tgz/.test(url)), false);
  assert.equal(catalog.artifacts[4].platform, 'windows/amd64');
  assert.equal(f.npm.has('windows-amd64'), true);
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
});

test('npm can publish the manifest-declared Windows ZIP independently of the four-Unix development template', async (t) => {
  const f = await releaseFixture(t, { npm: true, homebrew: true, npmPlatforms: platforms });
  const source = await f.copySources();
  const filename = path.join(source, 'npm/packages/cli/package.json');
  const template = JSON.parse(await readFile(filename, 'utf8'));
  delete template.optionalDependencies['@sodapop-sh/windows-amd64'];
  await writeFile(filename, JSON.stringify(template));
  f.npm.get('cli').sodapop.artifacts.reverse();
  const catalog = await resolveReleases(f.options);
  assert.equal(catalog.channels.npm.status, 'published');
  assert.deepEqual(catalog.channels.npm.platforms, platforms);
  assert.deepEqual(catalog.channels.homebrew.platforms, platforms.slice(0, 4));
  assert.equal(catalog.artifacts[4].archive, `sodapop-${version}-windows-amd64.zip`);
  assert.equal(catalog.artifacts[4].binary, 'sodapop.exe');
  const requests = f.requests.filter(({ url }) => url.startsWith(registry));
  assert.equal(requests.length, 7);
  assert.ok(requests.some(({ url }) => url === `${registry}/@sodapop-sh%2fwindows-amd64/${version}`));
  assert.equal(f.requests.some(({ url }) => /\.tgz$|\.zip$|\.tar\.gz$/.test(url)), false);
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
  const reordered = await snapshot(f);
  reordered.catalog.channels.npm.platforms.reverse();
  await writeFile(f.resolvedPath, JSON.stringify(reordered));
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
});

test('public npm metadata reports its exact nonempty declared subset without implying cross-platform readiness', async (t) => {
  for (const declared of [['darwin/arm64'], ['windows/amd64'], ['linux/amd64', 'darwin/arm64']]) {
    await t.test(declared.join(','), async (t) => {
      const f = await releaseFixture(t, { npm: true, npmPlatforms: declared });
      const catalog = await resolveReleases(f.options);
      const expected = platforms.filter((platform) => declared.includes(platform));
      assert.equal(catalog.channels.npm.status, 'published');
      assert.deepEqual(catalog.channels.npm.platforms, expected);
      assert.deepEqual(catalog.artifacts.map(({ platform }) => platform), platforms);
      assert.deepEqual(f.requests.filter(({ url }) => url.startsWith(registry)).map(({ url }) => url), [
        `${registry}/-/package/@sodapop-sh%2fcli/dist-tags`,
        `${registry}/@sodapop-sh%2fcli/${version}`,
        ...expected.map((platform) => `${registry}/@sodapop-sh%2f${platform.replace('/', '-')}/${version}`),
      ]);
      assert.deepEqual(await readCatalog(f.readOptions), catalog);
      await assert.rejects(readCatalog({ ...f.readOptions, allowFixtures: false }), /cannot use fixture/);
    });
  }
});

test('a missing declared Windows package keeps npm unpublished instead of silently advertising only Unix', async (t) => {
  const f = await releaseFixture(t, { npm: true, homebrew: true, npmPlatforms: platforms });
  missing(f, `${registry}/@sodapop-sh%2fwindows-amd64/${version}`);
  const catalog = await resolveReleases(f.options);
  assert.deepEqual(catalog.channels.npm, unavailable);
  assert.equal(catalog.channels.homebrew.status, 'published');
  assert.deepEqual(catalog.channels.homebrew.platforms, platforms.slice(0, 4));
  assert.equal(catalog.artifacts.length, 5);
  assert.ok(f.requests.some(({ url }) => url === `${registry}/@sodapop-sh%2fwindows-amd64/${version}`));
});

test('declared Windows npm metadata must match win32/x64 and the public ZIP/binary release binding', async (t) => {
  for (const [name, mutate] of [
    ['Go OS instead of npm OS', (pkg) => { pkg.os = ['windows']; }],
    ['Go architecture instead of npm CPU', (pkg) => { pkg.cpu = ['amd64']; }],
    ['wrong package', (pkg) => { pkg.name = '@sodapop-sh/windows-arm64'; }],
    ['wrong version', (pkg) => { pkg.version = '1.2.4'; }],
    ['tarball instead of ZIP', (pkg) => { pkg.sodapop.artifact.archive = pkg.sodapop.artifact.archive.replace('.zip', '.tar.gz'); }],
    ['archive digest', (pkg) => { pkg.sodapop.artifact.archive_sha256 = 'f'.repeat(64); }],
    ['binary digest', (pkg) => { pkg.sodapop.artifact.binary_sha256 = 'f'.repeat(64); }],
    ['runtime binding', (pkg) => { pkg.sodapop.copilot_runtime_version = '0.0.1'; }],
    ['missing binding', (pkg) => { delete pkg.sodapop; }],
  ]) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { npm: true, npmPlatforms: platforms });
      mutate(f.npm.get('windows-amd64'));
      await assert.rejects(resolveReleases(f.options), /npm/);
      await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
    });
  }
});

test('development optionalDependencies are not the published npm platform set', async (t) => {
  for (const names of [[], ['@sodapop-sh/windows-amd64'], null]) {
    await t.test(JSON.stringify(names), async (t) => {
      const f = await releaseFixture(t, { npm: true, npmPlatforms: platforms });
      const source = await f.copySources();
      const filename = path.join(source, 'npm/packages/cli/package.json');
      const template = JSON.parse(await readFile(filename, 'utf8'));
      if (names === null) delete template.optionalDependencies;
      else template.optionalDependencies = Object.fromEntries(names.map((name) => [name, template.version]));
      await writeFile(filename, JSON.stringify(template));
      const catalog = await resolveReleases(f.options);
      assert.deepEqual(catalog.channels.npm.platforms, platforms);
    });
  }
});

test('Windows npm source metadata uses win32/x64 and the executable payload filename', async (t) => {
  for (const [name, mutate] of [
    ['OS', (pkg) => { pkg.os = ['windows']; }],
    ['CPU', (pkg) => { pkg.cpu = ['amd64']; }],
    ['binary filename', (pkg) => { pkg.files = ['bin/sodapop']; }],
  ]) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { mode: 'pre-release' });
      const source = await f.copySources();
      const filename = path.join(source, 'npm/packages/windows-amd64/package.json');
      const pkg = JSON.parse(await readFile(filename, 'utf8'));
      mutate(pkg);
      await writeFile(filename, JSON.stringify(pkg));
      await assert.rejects(readCatalog(f.readOptions), /Source npm package identity\/platform differs for windows\/amd64/);
      assert.equal(f.requests.length, 0);
    });
  }
});

test('local native subsets cannot silently replace the five advertised public downloads', async (t) => {
  const f = await releaseFixture(t, { npm: true, npmPlatforms: ['darwin/arm64'] });
  f.manifest.artifacts = [f.manifest.artifacts[0]];
  f.refreshManifest();
  await assert.rejects(resolveReleases(f.options), /all five native platforms/);
  await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
});

test('missing npm launcher/platforms and a stale dist-tag keep npm unavailable but do not hide Homebrew', async (t) => {
  for (const name of ['missing launcher', 'partial native packages', 'stale dist-tag']) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { npm: true, homebrew: true });
      if (name === 'missing launcher') missing(f, `${registry}/@sodapop-sh%2fcli/${version}`);
      if (name === 'partial native packages') missing(f, `${registry}/@sodapop-sh%2flinux-arm64/${version}`);
      if (name === 'stale dist-tag') f.distTags.latest = '1.2.2';
      const catalog = await resolveReleases(f.options);
      assert.deepEqual(catalog.channels.npm, unavailable);
      assert.equal(catalog.channels.homebrew.status, 'published');
      assert.equal(catalog.artifacts.length, 5);
    });
  }
});

test('an exact published prerelease resolves native downloads and npm preview without Homebrew', async (t) => {
  const releaseVersion = '1.2.3-rc.9';
  const f = await releaseFixture(t, { npm: true, releaseVersion });
  const catalog = await resolveReleases(f.options);
  assert.equal(catalog.version, releaseVersion);
  assert.deepEqual(catalog.channels.npm, {
    status: 'published', command: 'npm install --global @sodapop-sh/cli@preview', version: releaseVersion,
    url: 'https://www.npmjs.com/package/@sodapop-sh/cli', platforms: platforms.slice(0, 4),
  });
  assert.deepEqual(catalog.channels.homebrew, unavailable);
  assert.ok(f.requests.some(({ url }) => url.endsWith('/dist-tags')));
  assert.ok(f.requests.some(({ url }) => url.endsWith(`/@sodapop-sh%2fcli/${releaseVersion}`)));
});

test('npm metadata rejects wrong identities, unsupported platforms, bad URLs, and manifest mismatches', async (t) => {
  const cases = [
    ['launcher identity', (f) => { f.npm.get('cli').name = '@different/cli'; }],
    ['repository', (f) => { f.npm.get('cli').repository.url = 'https://example.test/sodapop.git'; }],
    ['private package', (f) => { f.npm.get('cli').private = true; }],
    ['deprecated package', (f) => { f.npm.get('cli').deprecated = 'do not install'; }],
    ['command name', (f) => { f.npm.get('cli').bin = { other: 'bin/sodapop.js' }; }],
    ['launcher runtime', (f) => { f.npm.get('cli').engines.node = '>=10'; }],
    ['missing Unix package', (f) => { delete f.npm.get('cli').optionalDependencies['@sodapop-sh/linux-arm64']; }],
    ['undeclared Windows dependency', (f) => { f.npm.get('cli').optionalDependencies['@sodapop-sh/windows-amd64'] = version; }],
    ['range dependency', (f) => { f.npm.get('cli').optionalDependencies['@sodapop-sh/linux-arm64'] = `^${version}`; }],
    ['tarball host', (f) => { f.npm.get('cli').dist.tarball = 'https://example.test/cli.tgz'; }],
    ['tarball credentials', (f) => { f.npm.get('cli').dist.tarball += '?token=secret'; }],
    ['integrity', (f) => { f.npm.get('cli').dist.integrity = 'sha512-wrong'; }],
    ['newline integrity', (f) => { f.npm.get('cli').dist.integrity += '\n'; }],
    ['native identity', (f) => { f.npm.get('darwin-arm64').name = '@sodapop-sh/windows-amd64'; }],
    ['native version', (f) => { f.npm.get('darwin-arm64').version = '1.2.4'; }],
    ['native OS', (f) => { f.npm.get('darwin-arm64').os = ['windows']; }],
    ['native CPU', (f) => { f.npm.get('darwin-amd64').cpu = ['amd64']; }],
    ['native libc', (f) => { f.npm.get('linux-amd64').libc = ['musl']; }],
    ['launcher manifest missing', (f) => { delete f.npm.get('cli').sodapop; }],
    ['manifest/dependency subset mismatch', (f) => { f.npm.get('cli').sodapop.artifacts.pop(); }],
    ['Windows artifact without dependency', (f) => { f.npm.get('cli').sodapop.artifacts.push(f.manifest.artifacts[4]); }],
    ['empty manifest set', (f) => { f.npm.get('cli').sodapop.artifacts = []; f.npm.get('cli').optionalDependencies = {}; }],
    ['non-array manifest set', (f) => { f.npm.get('cli').sodapop.artifacts = {}; }],
    ['duplicate manifest platform', (f) => { f.npm.get('cli').sodapop.artifacts.push({ ...f.npm.get('cli').sodapop.artifacts[0] }); }],
    ['unsupported manifest platform', (f) => { f.npm.get('cli').sodapop.artifacts[0].platform = 'windows/arm64'; }],
    ['native archive', (f) => { f.npm.get('darwin-arm64').sodapop.artifact.archive = 'other.tar.gz'; }],
    ['native archive hash', (f) => { f.npm.get('darwin-arm64').sodapop.artifact.archive_sha256 = 'f'.repeat(64); }],
    ['native binary hash', (f) => { f.npm.get('darwin-arm64').sodapop.artifact.binary_sha256 = 'f'.repeat(64); }],
    ['native SDK', (f) => { f.npm.get('darwin-arm64').sodapop.copilot_sdk_version = '1.2.3'; }],
    ['native commit', (f) => { f.npm.get('darwin-arm64').sodapop.commit = 'f'.repeat(40); }],
    ['obsolete native metadata', (f) => { f.npm.get('darwin-arm64').sodapop = { releaseArchive: 'old', sha256: '0'.repeat(64) }; }],
    ['native metadata missing', (f) => { delete f.npm.get('darwin-arm64').sodapop; }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { npm: true });
      mutate(f);
      await assert.rejects(resolveReleases(f.options), /npm/);
    });
  }
});

test('public default-branch Homebrew formula must be the generated four-Unix-platform template', async (t) => {
  const f = await releaseFixture(t, { homebrew: true });
  const catalog = await resolveReleases(f.options);
  assert.deepEqual(catalog.channels.homebrew, {
    status: 'published', command: 'brew install VeVarunSharma/sodapop/sodapop', version,
    url: 'https://github.com/VeVarunSharma/homebrew-sodapop', platforms: platforms.slice(0, 4),
  });
  assert.deepEqual(catalog.channels.npm, unavailable);
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
});

test('missing/stale Homebrew formula leaves independently verified npm available', async (t) => {
  for (const stale of [false, true]) {
    await t.test(stale ? 'stale' : 'missing', async (t) => {
      const f = await releaseFixture(t, { homebrew: true, npm: true });
      if (stale) f.setFormula(f.formula.replaceAll(version, '1.2.2'));
      else missing(f, `${tapAPI}/contents/Formula/sodapop.rb`, 'github');
      const catalog = await resolveReleases(f.options);
      assert.deepEqual(catalog.channels.homebrew, unavailable);
      assert.equal(catalog.channels.npm.status, 'published');
    });
  }
});

test('Homebrew wrong repositories, formula content, URLs, hashes, and unresolved access fail closed', async (t) => {
  const cases = [
    ['tap access unresolved', (f) => { missing(f, tapAPI, 'github'); }],
    ['private tap', (f) => { f.tap.private = true; }],
    ['wrong tap', (f) => { f.tap.full_name = 'other/homebrew-sodapop'; }],
    ['wrong branch', (f) => { f.tap.default_branch = 'other'; }],
    ['symlink', (f) => { f.formulaFile.type = 'symlink'; }],
    ['path', (f) => { f.formulaFile.path = 'Formula/other.rb'; }],
    ['encoding', (f) => { f.formulaFile.content += '$'; }],
    ['blob digest', (f) => { f.formulaFile.sha = '0'.repeat(40); }],
    ['file size', (f) => { f.formulaFile.size += 1; }],
    ['formula host', (f) => { f.formulaFile.html_url = 'https://example.test/sodapop.rb'; }],
    ['raw host', (f) => { f.formulaFile.download_url = 'https://example.test/sodapop.rb'; }],
    ['unknown formula behavior', (f) => { f.setFormula(`${f.formula}\nsystem "do-something"\n`); }],
    ['wrong class', (f) => { f.setFormula(f.formula.replace('class Sodapop', 'class Other')); }],
    ['wrong archive host', (f) => { f.setFormula(f.formula.replace('github.com', 'example.test')); }],
    ['wrong platform', (f) => { f.setFormula(f.formula.replace('darwin-arm64', 'windows-amd64')); }],
    ['missing platform', (f) => { f.setFormula(f.formula.replace(/^      sha256 .*$/m, '')); }],
    ['hash mismatch', (f) => { f.setFormula(f.formula.replace(f.manifest.artifacts[0].archive_sha256, 'f'.repeat(64))); }],
    ['SDK mismatch', (f) => { f.setFormula(f.formula.replaceAll(f.manifest.copilot_sdk_version, '1.0.12')); }],
    ['runtime mismatch', (f) => { f.setFormula(f.formula.replaceAll(f.manifest.copilot_runtime_version, '1.0.82')); }],
    ['malformed stale SDK', (f) => {
      f.setFormula(f.formula.replaceAll(version, '1.2.2').replaceAll(f.manifest.copilot_sdk_version, 'development'));
    }],
    ['placeholder version', (f) => { f.setFormula(f.formula.replaceAll(version, '0.0.0')); }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { homebrew: true });
      mutate(f);
      await assert.rejects(resolveReleases(f.options), /Homebrew/);
    });
  }
});

test('network/auth/rate-limit failures never become unpublished or pre-release success', async (t) => {
  for (const [name, url, status, body] of [
    ['unresolved canonical repo', api, 404, { message: 'Not Found' }],
    ['release unauthorized', `${api}/releases/latest`, 401, {}],
    ['release forbidden', `${api}/releases/latest`, 403, {}],
    ['GitHub rate limit', api, 429, {}],
    ['GitHub server error', api, 500, {}],
    ['npm dist-tag unauthorized', `${registry}/-/package/@sodapop-sh%2fcli/dist-tags`, 401, {}],
    ['npm dist-tag rate limit', `${registry}/-/package/@sodapop-sh%2fcli/dist-tags`, 429, {}],
    ['npm unauthorized', `${registry}/@sodapop-sh%2fcli/${version}`, 401, {}],
    ['npm forbidden', `${registry}/@sodapop-sh%2fcli/${version}`, 403, {}],
    ['npm rate limit', `${registry}/@sodapop-sh%2fcli/${version}`, 429, {}],
    ['ambiguous npm absence', `${registry}/@sodapop-sh%2fcli/${version}`, 404, { error: 'Authentication required' }],
    ['tap server error', tapAPI, 503, {}],
    ['ambiguous formula absence', `${tapAPI}/contents/Formula/sodapop.rb`, 404, { message: 'Access denied' }],
  ]) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { npm: true, homebrew: true });
      f.setJSON(url, body, status);
      await assert.rejects(resolveReleases(f.options), /HTTP|ambiguous 404/);
      await assert.rejects(readFile(f.resolvedPath), { code: 'ENOENT' });
    });
  }
});

test('only bounded explicit asset-CDN redirects are followed, without credentials or URL serialization', async (t) => {
  const f = await releaseFixture(t);
  const original = f.routes.get(manifestURL);
  const cdn = 'https://release-assets.githubusercontent.com/github-production-release-asset/123/asset-id?token=do-not-serialize';
  f.routes.set(manifestURL, () => new Response(null, { status: 302, headers: { location: cdn } }));
  f.routes.set(cdn, original);
  await resolveReleases(f.options);
  assert.ok(f.requests.some(({ url }) => url === cdn));
  for (const { options } of f.requests) {
    assert.equal(options.redirect, 'manual');
    assert.equal(options.credentials, 'omit');
    assert.equal(Object.keys(options.headers).some((key) => /authorization|cookie/i.test(key)), false);
  }
  const data = await readFile(f.resolvedPath, 'utf8');
  assert.equal(data.includes('do-not-serialize'), false);
  assert.equal(data.includes('githubusercontent.com'), false);
  f.routes.set(cdn, () => new Response(null, { status: 302, headers: { location: cdn } }));
  await assert.rejects(resolveReleases(f.options), /redirect limit/);
  assert.equal(await readFile(f.resolvedPath, 'utf8'), data);
});

test('API redirects, malicious CDN locations, and fetch auto-follow cannot change the trusted host', async (t) => {
  for (const location of [
    'https://example.test/manifest.json',
    'https://release-assets.githubusercontent.com.example.test/github-production-release-asset/123/id',
    'http://release-assets.githubusercontent.com/github-production-release-asset/123/id',
    'https://user:secret@release-assets.githubusercontent.com/github-production-release-asset/123/id',
    'https://release-assets.githubusercontent.com/other/123/id',
    'https://release-assets.githubusercontent.com/github-production-release-asset/123/id#fragment',
    '/login',
  ]) {
    await t.test(location, async (t) => {
      const f = await releaseFixture(t);
      f.routes.set(manifestURL, () => new Response(null, { status: 302, headers: { location } }));
      await assert.rejects(resolveReleases(f.options), /unsafe redirect/);
      assert.equal(f.requests.length, 3);
    });
  }
  const f = await releaseFixture(t);
  f.routes.set(api, () => new Response(null, { status: 302, headers: { location: 'https://github.com/login' } }));
  await assert.rejects(resolveReleases(f.options), /unsafe redirect/);
  f.routes.set(api, () => {
    const response = Response.json(f.repository);
    Object.defineProperty(response, 'url', { value: 'https://example.test/moved' });
    return response;
  });
  await assert.rejects(resolveReleases(f.options), /unvalidated redirect/);
});

test('requests and streamed/local metadata reads are bounded and malformed responses are errors', async (t) => {
  const cases = [
    ['invalid JSON', () => new Response('{invalid')],
    ['duplicate JSON metadata', () => new Response('{"private":true,"private":false}')],
    ['invalid UTF-8', () => new Response(Uint8Array.of(0xff))],
    ['declared excessive size', () => new Response('{}', { headers: { 'content-length': '262145' } })],
    ['undeclared excessive size', () => new Response(' '.repeat(262145))],
    ['streamed excessive size', () => new Response(new ReadableStream({
      start(controller) { controller.enqueue(Buffer.alloc(200000)); controller.enqueue(Buffer.alloc(100000)); controller.close(); },
    }))],
    ['empty body', () => new Response(null)],
    ['unexpected partial response', () => new Response('{}', { status: 206 })],
  ];
  for (const [name, response] of cases) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t);
      f.routes.set(api, response);
      await assert.rejects(resolveReleases(f.options), /JSON|limit|empty|HTTP/);
    });
  }
  const f = await releaseFixture(t);
  f.routes.set(api, () => new Promise(() => {}));
  await assert.rejects(resolveReleases({ ...f.options, timeoutMs: 10 }), /timed out/);
  assert.equal(f.requests[0].options.signal.aborted, true);
  await assert.rejects(resolveReleases({ ...f.options, timeoutMs: 0 }), /timeoutMs/);
  await writeFile(f.configPath, ' '.repeat(32769));
  await assert.rejects(readCatalog(f.readOptions), /32768 bytes/);
});

test('fetch/stream errors do not expose bearer values or signed URLs', async (t) => {
  const f = await releaseFixture(t);
  const secret = 'fixture-sensitive-value';
  f.routes.set(api, () => { throw new Error(`Bearer ${secret} https://example.test?token=${secret}`); });
  await assert.rejects(resolveReleases(f.options), (error) => {
    assert.match(error.message, /request failed/);
    assert.equal(String(error).includes(secret), false);
    return true;
  });
  f.routes.set(api, () => new Response(new ReadableStream({
    start(controller) { controller.error(new Error(secret)); },
  })));
  await assert.rejects(resolveReleases(f.options), (error) => !String(error).includes(secret));
});

test('source package and tap identity drift is rejected even when channels are disabled', async (t) => {
  for (const name of ['launcher', 'launcher joined keys', 'native', 'formula']) {
    await t.test(name, async (t) => {
      const f = await releaseFixture(t, { mode: 'pre-release' });
      const source = await f.copySources();
      if (name === 'formula') {
        const filename = path.join(source, 'packaging/homebrew/Formula/sodapop.rb.tmpl');
        await writeFile(filename, f.template.replaceAll('@RELEASE_BASE@', 'https://example.test/other/repo'));
      } else {
        const filename = path.join(source, `npm/packages/${name.startsWith('launcher') ? 'cli' : 'darwin-amd64'}/package.json`);
        const pkg = JSON.parse(await readFile(filename, 'utf8'));
        if (name === 'launcher') pkg.optionalDependencies['@sodapop-sh/windows-arm64'] = pkg.version;
        else if (name === 'launcher joined keys') {
          pkg.optionalDependencies = { [Object.keys(pkg.optionalDependencies).sort().join('\n')]: pkg.version };
        }
        else pkg.cpu = ['arm64'];
        await writeFile(filename, JSON.stringify(pkg));
      }
      await assert.rejects(resolveReleases(f.options), /Source/);
      assert.equal(f.requests.length, 0);
    });
  }
});

test('production readers reject fixture envelopes and injected resolvers cannot overwrite the production path', async (t) => {
  const f = await releaseFixture(t);
  await resolveReleases(f.options);
  await assert.rejects(readCatalog({ ...f.readOptions, allowFixtures: false }), /cannot use fixture/);
  await assert.rejects(resolveReleases({ ...f.options, resolvedPath: catalogPaths.resolved }), /refusing to overwrite/);
  assert.equal((await readCatalog(f.readOptions)).version, version);
});

test('offline release catalogs are revalidated for every build, including commands, versions, and exact URLs', async (t) => {
  const f = await releaseFixture(t, { npm: true, homebrew: true });
  await resolveReleases(f.options);
  const valid = await snapshot(f);
  const cases = [
    ['envelope marker', (r) => { r.source = 'development'; }],
    ['envelope repository', (r) => { r.repository = 'other/repo'; }],
    ['envelope schema', (r) => { r.schemaVersion = 2; }],
    ['timestamp', (r) => { r.checkedAt = 'not-a-date'; }],
    ['secret envelope field', (r) => { r.token = 'do-not-serialize'; }],
    ['catalog mode', (r) => { r.catalog.mode = 'pre-release'; }],
    ['catalog version', (r) => { r.catalog.version = '0.0.0'; }],
    ['unknown catalog field', (r) => { r.catalog.fixture = true; }],
    ['persisted preview marker', (r) => { r.catalog.preview = true; }],
    ['manifest URL', (r) => { r.catalog.manifestUrl = 'https://example.test/manifest.json'; }],
    ['release URL', (r) => { r.catalog.releaseUrl += '?secret=value'; }],
    ['archive URL', (r) => { r.catalog.artifacts[0].archiveUrl += '#not-exact'; }],
    ['checksum URL', (r) => { r.catalog.artifacts[0].checksumUrl = r.catalog.artifacts[1].checksumUrl; }],
    ['binary name', (r) => { r.catalog.artifacts[4].binary = 'sodapop'; }],
    ['binary hash', (r) => { r.catalog.artifacts[0].binarySha256 = 'no'; }],
    ['platform set', (r) => { r.catalog.artifacts.pop(); }],
    ['format', (r) => { r.catalog.artifacts[0].format = 'zip'; }],
    ['display label', (r) => { r.catalog.artifacts[0].label = '<script>bad</script>'; }],
    ['unexpected artifact field', (r) => { r.catalog.artifacts[0].fixture = true; }],
    ['npm command injection', (r) => { r.catalog.channels.npm.command += '; echo unsafe'; }],
    ['tap command', (r) => { r.catalog.channels.homebrew.command = 'brew install other/tap/sodapop'; }],
    ['channel version mismatch', (r) => { r.catalog.channels.npm.version = '1.2.4'; }],
    ['missing declared platforms', (r) => { delete r.catalog.channels.npm.platforms; }],
    ['empty published platforms', (r) => { r.catalog.channels.npm.platforms = []; }],
    ['non-array platforms', (r) => { r.catalog.channels.npm.platforms = 'darwin/arm64'; }],
    ['unknown npm platform', (r) => { r.catalog.channels.npm.platforms = ['windows/arm64']; }],
    ['duplicate npm platform', (r) => { r.catalog.channels.npm.platforms = ['darwin/arm64', 'darwin/arm64']; }],
    ['Windows Homebrew platform', (r) => { r.catalog.channels.homebrew.platforms = platforms; }],
    ['incomplete Homebrew platforms', (r) => { r.catalog.channels.homebrew.platforms = ['darwin/arm64']; }],
    ['unpublished platform claims', (r) => { r.catalog.channels.npm = { ...unavailable, platforms: ['darwin/arm64'] }; }],
    ['channel URL', (r) => { r.catalog.channels.npm.url = 'https://example.test/'; }],
    ['unknown publication status', (r) => { r.catalog.channels.npm.status = 'ready'; }],
    ['unpublished command', (r) => { r.catalog.channels.npm.status = 'unpublished'; }],
    ['extra channel', (r) => { r.catalog.channels.winget = { ...unavailable }; }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(name, async () => {
      const value = structuredClone(valid);
      mutate(value);
      await writeFile(f.resolvedPath, JSON.stringify(value));
      await assert.rejects(readCatalog(f.readOptions));
    });
  }
  await writeFile(f.resolvedPath, JSON.stringify(valid));
  f.configuration.channels.npm.verify = false;
  await f.saveConfiguration();
  await assert.rejects(readCatalog(f.readOptions), /Published npm channel/);
  await writeFile(f.resolvedPath, ' '.repeat(131073));
  await assert.rejects(readCatalog(f.readOptions), /131072 bytes/);
});

test('failed refreshes and failed atomic replacement preserve the previous validated snapshot and clean staging', async (t) => {
  const f = await releaseFixture(t);
  const catalog = await resolveReleases(f.options);
  const original = await readFile(f.resolvedPath, 'utf8');
  const route = f.routes.get(api);
  f.routes.set(api, () => { throw new Error('network failure'); });
  await assert.rejects(resolveReleases(f.options), /request failed/);
  assert.equal(await readFile(f.resolvedPath, 'utf8'), original);
  f.routes.set(api, route);
  f.manifest.artifacts[0].binary_sha256 = 'bad';
  f.refreshManifest();
  await assert.rejects(resolveReleases(f.options), /binary_sha256/);
  assert.equal(await readFile(f.resolvedPath, 'utf8'), original);
  f.manifest.artifacts[0].binary_sha256 = catalog.artifacts[0].binarySha256;
  f.refreshManifest();
  await assert.rejects(resolveReleases({
    ...f.options,
    replaceFile: async (temporary, destination) => {
      assert.equal(path.dirname(temporary), path.dirname(destination));
      assert.equal(JSON.parse(await readFile(temporary, 'utf8')).catalog.version, version);
      throw new Error('simulated atomic rename failure');
    },
  }), /atomic rename failure/);
  assert.equal(await readFile(f.resolvedPath, 'utf8'), original);
  assert.deepEqual(await readdir(path.dirname(f.resolvedPath)), ['releases.json']);
  assert.deepEqual(await readCatalog(f.readOptions), catalog);
});

test('CLI has an explicit offline preview and accepts no fixture, endpoint, or credential switches', () => {
  const script = fileURLToPath(new URL('../../scripts/resolve-releases.mjs', import.meta.url));
  const args = ['--import', subprocessGuard, script];
  const result = execFileSync(process.execPath, args, {
    encoding: 'utf8',
    env: { ...process.env, ...previewEnvironment, GH_TOKEN: 'fixture-do-not-use', SODAPOP_SITE_RELEASE_MODE: 'release' },
  });
  assert.match(result, /explicit offline preview.*no network requests or release metadata writes/);
  assert.equal(result.includes('fixture-do-not-use'), false);
  const help = execFileSync(process.execPath, [...args, '--help'], { encoding: 'utf8' });
  assert.match(help, /channels\.json/);
  assert.match(help, /SODAPOP_SITE_PREVIEW/);
  assert.throws(() => execFileSync(process.execPath, [...args, '--fixture'], { stdio: 'pipe' }), /Unsupported arguments/);
  assert.throws(() => execFileSync(process.execPath, args, {
    stdio: 'pipe', env: { ...process.env, SODAPOP_SITE_PREVIEW: 'true' },
  }), /SODAPOP_SITE_PREVIEW must be 0 or 1/);
});
