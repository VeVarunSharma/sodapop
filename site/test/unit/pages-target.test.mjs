import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { execFileSync, spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pagesBuildTarget } from '../../scripts/pages-target.mjs';

const defaults = { variant: 'configured', repository: 'VeVarunSharma/sodapop', requirePages: true };
const project = { origin: 'https://vevarunsharma.github.io', base: '/sodapop/' };

test('production follows actual project Pages metadata without custom-domain assumptions', () => {
  assert.deepEqual(pagesBuildTarget({ ...defaults, pagesUrl: 'https://vevarunsharma.github.io/sodapop' }), project);
  assert.deepEqual(pagesBuildTarget({ ...defaults, pagesUrl: 'https://sodapop.sh' }), { origin: 'https://sodapop.sh', base: '/' });
});

test('explicit site overrides must agree with actual hosting', () => {
  const options = { ...defaults, pagesUrl: 'https://vevarunsharma.github.io/sodapop/' };
  assert.deepEqual(pagesBuildTarget({ ...options, expectedOrigin: project.origin, expectedBase: '/sodapop' }), project);
  assert.throws(() => pagesBuildTarget({ ...options, expectedOrigin: 'https://sodapop.sh' }), /overrides do not match/);
  assert.throws(() => pagesBuildTarget({ ...options, expectedBase: '/' }), /overrides do not match/);
});

test('offline reviews need no Pages access but deployment cannot silently fall back', () => {
  assert.throws(() => pagesBuildTarget(defaults), /requires metadata/);
  assert.deepEqual(pagesBuildTarget({ ...defaults, requirePages: false }), { origin: 'https://sodapop.sh', base: '/' });
  assert.deepEqual(pagesBuildTarget({ ...defaults, variant: 'project' }), project);
  assert.deepEqual(pagesBuildTarget({ ...defaults, variant: 'project', repository: 'Owner/website.docs' }), {
    origin: 'https://owner.github.io', base: '/website.docs/',
  });
  assert.deepEqual(pagesBuildTarget({ ...defaults, variant: 'project', repository: 'Owner/Owner.github.io' }), {
    origin: 'https://owner.github.io', base: '/',
  });
});

test('invalid metadata cannot become a publication target or environment injection', () => {
  for (const pagesUrl of ['invalid', 'http://sodapop.sh', 'https://user:pass@sodapop.sh/', 'https://sodapop.sh/?x=1', 'https://sodapop.sh/#x', 'https://sodapop.sh/\nBAD=1', 'https://sodapop.sh/%0aBAD=1']) {
    assert.throws(() => pagesBuildTarget({ ...defaults, pagesUrl }));
  }
  assert.throws(() => pagesBuildTarget({ ...defaults, variant: 'unknown' }), /variant/);
  assert.throws(() => pagesBuildTarget({ ...defaults, repository: 'owner/../repo' }), /GITHUB_REPOSITORY/);
});

test('workflow helper writes validated values and preserves the environment file on failure', async (t) => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'sodapop-pages-target-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const environmentFile = path.join(directory, 'github-env');
  const script = fileURLToPath(new URL('../../scripts/pages-target.mjs', import.meta.url));
  await writeFile(environmentFile, 'EXISTING=value\n');
  const env = {
    ...process.env,
    GITHUB_ENV: environmentFile,
    GITHUB_REPOSITORY: defaults.repository,
    SODAPOP_SITE_VARIANT: 'configured',
    SODAPOP_PAGES_REQUIRED: '1',
    SODAPOP_PAGES_URL: 'https://vevarunsharma.github.io/sodapop/',
    SODAPOP_EXPECTED_SITE_URL: '',
    SODAPOP_EXPECTED_SITE_BASE: '',
  };
  execFileSync(process.execPath, [script], { env });
  const written = await readFile(environmentFile, 'utf8');
  assert.equal(written, `EXISTING=value\nSODAPOP_SITE_URL=${project.origin}\nSODAPOP_SITE_BASE=${project.base}\n`);
  const failed = spawnSync(process.execPath, [script], { env: { ...env, SODAPOP_PAGES_URL: '' }, encoding: 'utf8' });
  assert.notEqual(failed.status, 0);
  assert.match(failed.stderr, /requires metadata/);
  assert.equal(await readFile(environmentFile, 'utf8'), written);
});
