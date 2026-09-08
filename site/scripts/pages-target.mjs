import { appendFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { normalizeBase, siteConfiguration } from './site-config.mjs';

/**
 * The configured matrix builds the actual Pages target. The project matrix
 * deliberately exercises a repository prefix even when a custom domain is used.
 * @param {{variant: string, repository: string, pagesUrl?: string, expectedOrigin?: string, expectedBase?: string, requirePages?: boolean}} options
 */
export function pagesBuildTarget({
  variant, repository, pagesUrl = '', expectedOrigin = '', expectedBase = '', requirePages = false,
}) {
  if (!['configured', 'project'].includes(variant)) throw new Error('Unknown website publication variant');
  const match = /^([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9._-]+)$/.exec(repository || '');
  if (!match || ['.', '..'].includes(match[2])) throw new Error('GITHUB_REPOSITORY must identify an owner and repository');
  const [, owner, repo] = match;

  if (variant === 'project') {
    return {
      origin: `https://${owner.toLowerCase()}.github.io`,
      base: repo.toLowerCase() === `${owner.toLowerCase()}.github.io` ? '/' : normalizeBase(`/${repo}/`),
    };
  }

  if (!pagesUrl) {
    if (requirePages) throw new Error('A deployment build requires metadata from actions/configure-pages');
    return siteConfiguration({
      SODAPOP_SITE_URL: expectedOrigin || undefined,
      SODAPOP_SITE_BASE: expectedBase || undefined,
    });
  }

  if (/[\u0000-\u0020\u007f]/.test(pagesUrl) || !URL.canParse(pagesUrl)) {
    throw new Error('GitHub Pages returned an invalid publication URL');
  }
  const url = new URL(pagesUrl);
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) {
    throw new Error('GitHub Pages must use an HTTPS URL without credentials, queries, or fragments');
  }
  const target = { origin: url.origin, base: normalizeBase(url.pathname) };
  const expected = siteConfiguration({
    SODAPOP_SITE_URL: expectedOrigin || target.origin,
    SODAPOP_SITE_BASE: expectedBase || target.base,
  });
  if (expected.origin !== target.origin || expected.base !== target.base) {
    throw new Error('Repository site overrides do not match GitHub Pages. Update the Pages custom domain or remove stale SODAPOP_SITE_URL/SODAPOP_SITE_BASE overrides.');
  }
  return target;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const required = process.env.SODAPOP_PAGES_REQUIRED || '0';
  if (!['0', '1'].includes(required)) throw new Error('SODAPOP_PAGES_REQUIRED must be 0 or 1');
  if (!process.env.GITHUB_ENV) throw new Error('This workflow helper requires GITHUB_ENV');
  const target = pagesBuildTarget({
    variant: process.env.SODAPOP_SITE_VARIANT,
    repository: process.env.GITHUB_REPOSITORY,
    pagesUrl: process.env.SODAPOP_PAGES_URL,
    expectedOrigin: process.env.SODAPOP_EXPECTED_SITE_URL,
    expectedBase: process.env.SODAPOP_EXPECTED_SITE_BASE,
    requirePages: required === '1',
  });
  await appendFile(process.env.GITHUB_ENV, `SODAPOP_SITE_URL=${target.origin}\nSODAPOP_SITE_BASE=${target.base}\n`);
  console.log(`Website target: ${new URL(target.base, target.origin).href}`);
}
