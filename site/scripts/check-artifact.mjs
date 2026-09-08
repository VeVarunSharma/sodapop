import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { parse } from 'parse5';
import { siteRoot, siteConfiguration } from './site-config.mjs';

function elements(node, visit) {
  if (node.tagName) visit(node);
  for (const child of node.childNodes || []) elements(child, visit);
  if (node.content) elements(node.content, visit);
}

function documentInfo(html) {
  const ids = new Set();
  const links = [];
  let mode;
  elements(parse(html), (node) => {
    const attributes = Object.fromEntries((node.attrs || []).map(({ name, value }) => [name, value]));
    if (attributes.id) ids.add(attributes.id);
    if (node.tagName === 'meta' && attributes.name === 'sodapop-build') mode = attributes.content;
    for (const attribute of ['href', 'src']) {
      if (attributes[attribute]) links.push(attributes[attribute]);
    }
    if (attributes.srcset && !attributes.srcset.startsWith('data:')) {
      for (const candidate of attributes.srcset.split(',')) links.push(candidate.trim().split(/\s+/)[0]);
    }
  });
  return { ids, links, mode };
}

export async function checkArtifact(options = {}) {
  const root = path.resolve(options.root || path.join(siteRoot, 'dist'));
  const { base, origin } = options.publication || siteConfiguration();
  const requirePublic = options.requirePublic || false;
  const files = new Map();
  async function walk(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      const relative = path.relative(root, absolute).split(path.sep).join('/');
      if (entry.isSymbolicLink()) throw new Error(`Pages artifacts cannot contain symlinks: ${relative}`);
      if (relative.split('/').some((part) => /^(?:node_modules|\.git|\.github|\.generated|internal|npm|src|scripts|AGENTS\.md|copilot-instructions\.md|agent-guidance|\.env(?:\..*)?)$/i.test(part))) {
        throw new Error(`Non-public repository content in Pages artifact: ${relative}`);
      }
      if (entry.isDirectory()) {
        await walk(absolute);
      } else if (entry.isFile()) {
        if (!/^(?:_astro\/|assets\/|licenses\/|pagefind\/|docs\/|download\/|index\.html$|404\.html$|robots\.txt$|sitemap(?:-index|-\d+)?\.xml$|\.nojekyll$)/.test(relative)) {
          throw new Error(`Unexpected Pages artifact file: ${relative}`);
        }
        files.set(relative, { absolute, size: (await stat(absolute)).size });
      } else {
        throw new Error(`Unsupported artifact entry: ${relative}`);
      }
    }
  }
  await walk(root);
  for (const required of ['index.html', 'download/index.html', 'docs/index.html', '404.html', 'robots.txt']) {
    if (!files.has(required)) throw new Error(`Missing required static route: ${required}`);
  }
  if (![...files.keys()].some((file) => file.startsWith('pagefind/') && file.endsWith('.js'))) {
    throw new Error('The built documentation search index is missing');
  }
  const documents = new Map();
  for (const [file, metadata] of files) {
    if (!file.endsWith('.html')) continue;
    const info = documentInfo(await readFile(metadata.absolute, 'utf8'));
    if (!['public', 'preview'].includes(info.mode)) throw new Error(`Missing build identity: ${file}`);
    if (requirePublic && info.mode !== 'public') throw new Error(`Refusing to deploy preview HTML: ${file}`);
    documents.set(file, info);
  }
  for (const [file, info] of documents) {
    const pagePath = file === 'index.html' ? '' : file.replace(/index\.html$/, '');
    const pageUrl = new URL(`${base}${pagePath}`, origin);
    for (const value of info.links) {
      const link = new URL(value, pageUrl);
      if (['mailto:', 'tel:', 'data:'].includes(link.protocol)) continue;
      if (!['https:', 'http:'].includes(link.protocol)) throw new Error(`Unsafe URL in ${file}: ${value}`);
      if (link.origin !== origin) continue;
      if (!link.pathname.startsWith(base)) throw new Error(`Link escapes the configured site base in ${file}: ${value}`);
      let target = decodeURIComponent(link.pathname.slice(base.length));
      if (target === '' || target.endsWith('/')) target += 'index.html';
      else if (!path.posix.extname(target)) target += '/index.html';
      if (target.split('/').includes('..') || !files.has(target)) {
        throw new Error(`Broken local target in ${file}: ${value}`);
      }
      if (link.hash && documents.has(target) && !documents.get(target).ids.has(decodeURIComponent(link.hash.slice(1)))) {
        throw new Error(`Broken local anchor in ${file}: ${value}`);
      }
    }
  }
  const totalBytes = [...files.values()].reduce((total, file) => total + file.size, 0);
  if (totalBytes > 32 * 1024 * 1024) throw new Error('The static artifact exceeds the 32 MiB whole-site budget');
  return { pages: documents.size, files: files.size, bytes: totalBytes };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const result = await checkArtifact({ requirePublic: process.argv.includes('--public') });
  console.log(`Static artifact: ${result.pages} pages, ${result.files} files, ${result.bytes} bytes; local targets are intact.`);
}
