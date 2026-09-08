import test from 'node:test';
import assert from 'node:assert/strict';
import { assertPagesTarget, isPreview, normalizeBase, siteConfiguration } from '../../scripts/site-config.mjs';

test('normalizes root and repository-prefixed builds', () => {
  assert.equal(normalizeBase(), '/');
  assert.equal(normalizeBase(''), '/');
  assert.equal(normalizeBase('/sodapop'), '/sodapop/');
  assert.equal(normalizeBase('/sodapop/'), '/sodapop/');
  assert.equal(normalizeBase('/website.docs/'), '/website.docs/');
  assert.deepEqual(siteConfiguration({}), { origin: 'https://sodapop.sh', base: '/' });
  assert.deepEqual(siteConfiguration({
    SODAPOP_SITE_URL: 'https://vevarunsharma.github.io',
    SODAPOP_SITE_BASE: '/sodapop',
  }), { origin: 'https://vevarunsharma.github.io', base: '/sodapop/' });
});

test('preview mode requires an explicit flag', () => {
  assert.equal(isPreview({}), false);
  assert.equal(isPreview({ SODAPOP_SITE_PREVIEW: '0' }), false);
  assert.equal(isPreview({ SODAPOP_SITE_PREVIEW: '1' }), true);
  assert.throws(() => isPreview({ SODAPOP_SITE_PREVIEW: 'yes' }), /SODAPOP_SITE_PREVIEW/);
  assert.throws(() => isPreview({ SODAPOP_SITE_PREVIEW: '' }), /SODAPOP_SITE_PREVIEW/);
});

test('rejects ambiguous or unsafe publication paths and origins', () => {
  for (const base of ['sodapop', '//evil.example', '/..', '/.', '/foo/./bar', '/foo/../bar', '/foo?bar', '/foo#bar', '/foo\\bar']) {
    assert.throws(() => normalizeBase(base), /SODAPOP_SITE_BASE/);
  }
  for (const origin of ['not-a-url', 'http://sodapop.sh', 'https://user:pass@sodapop.sh', 'https://sodapop.sh/path', 'https://sodapop.sh/?x=1']) {
    assert.throws(() => siteConfiguration({ SODAPOP_SITE_URL: origin }), /SODAPOP_SITE_URL/);
  }
});

test('deployment target must match the configured canonical site and base', () => {
  const root = { origin: 'https://sodapop.sh', base: '/' };
  assert.doesNotThrow(() => assertPagesTarget('https://sodapop.sh/', root));
  const project = { origin: 'https://vevarunsharma.github.io', base: '/sodapop/' };
  assert.doesNotThrow(() => assertPagesTarget('https://vevarunsharma.github.io/sodapop', project));
  assert.throws(() => assertPagesTarget('https://vevarunsharma.github.io/sodapop/', root), /does not match/);
  assert.throws(() => assertPagesTarget('https://vevarunsharma.github.io/wrong/', project), /does not match/);
  assert.throws(() => assertPagesTarget(''), /valid publication URL/);
});
