import { fileURLToPath } from 'node:url';
import path from 'node:path';

export const siteRoot = fileURLToPath(new URL('../', import.meta.url));
export const repositoryRoot = path.resolve(siteRoot, '..');

export function isPreview(environment = process.env) {
  const value = environment.SODAPOP_SITE_PREVIEW;
  if (value === undefined || value === '0') return false;
  if (value === '1') return true;
  throw new Error('SODAPOP_SITE_PREVIEW must be 0 or 1');
}

export function normalizeBase(value = '/') {
  if (value === '' || value === '/') return '/';
  if (!/^\/[A-Za-z0-9._-]+(?:\/[A-Za-z0-9._-]+)*\/?$/.test(value) ||
      value.split('/').some((segment) => segment === '.' || segment === '..')) {
    throw new Error('SODAPOP_SITE_BASE must be a root-relative path without dot segments, queries, or fragments');
  }
  return `${value.replace(/\/$/, '')}/`;
}

export function siteConfiguration(environment = process.env) {
  const value = environment.SODAPOP_SITE_URL || 'https://sodapop.sh';
  if (!URL.canParse(value)) throw new Error('SODAPOP_SITE_URL must be a valid HTTPS origin');
  const origin = new URL(value);
  if (origin.protocol !== 'https:' || origin.username || origin.password ||
      origin.pathname !== '/' || origin.search || origin.hash) {
    throw new Error('SODAPOP_SITE_URL must be an HTTPS origin, without credentials or a path');
  }
  return { origin: origin.origin, base: normalizeBase(environment.SODAPOP_SITE_BASE) };
}

export function assertPagesTarget(value, publication = siteConfiguration()) {
  if (!value || !URL.canParse(value)) throw new Error('GitHub Pages did not return a valid publication URL');
  const target = new URL(value).href.replace(/\/$/, '');
  const expected = new URL(publication.base, publication.origin).href.replace(/\/$/, '');
  if (target !== expected) {
    throw new Error('Configured website origin/base does not match GitHub Pages. Review SODAPOP_SITE_URL, SODAPOP_SITE_BASE, and the Pages custom domain.');
  }
}
