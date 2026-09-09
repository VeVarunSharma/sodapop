import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkStringify from 'remark-stringify';
import { visit } from 'unist-util-visit';
import {
  normalizeBase,
  repositoryRoot as defaultRepositoryRoot,
  siteConfiguration,
  siteRoot as defaultSiteRoot,
} from './site-config.mjs';
import {
  publicSourcePath,
  readRepositoryFile,
  repositoryFile,
  replaceOwnedDirectory,
} from './content-files.mjs';
import { publicAssets } from './prepare-assets.mjs';

export const installationMarker = '<!-- SODAPOP_INSTALLATION_REFERENCE -->';
export const canonicalRepository = 'https://github.com/VeVarunSharma/sodapop';
const mapPath = 'src/data/public-content.json';
const outputDirectory = 'src/content/docs/docs';
const markdown = unified().use(remarkParse).use(remarkStringify, {
  bullet: '-',
  fences: true,
  listItemIndent: 'one',
  resourceLink: true,
}).freeze();

function record(value, label, keys) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
  for (const key of Object.keys(value)) {
    if (!keys.includes(key)) throw new Error(`Unknown ${label} field: ${key}`);
  }
}

function text(value, label) {
  if (typeof value !== 'string' || !value.trim() || /[\u0000-\u001f\u007f]/u.test(value)) {
    throw new Error(`${label} must be nonempty single-line text`);
  }
}

function generatedFilename(page, pages) {
  const slug = page.route.slice('/docs/'.length).replace(/\/$/, '');
  if (!slug) return 'index.md';
  return pages.some((other) => other !== page && other.route.startsWith(page.route))
    ? `${slug}/index.md`
    : `${slug}.md`;
}

function validateMap(map) {
  record(map, 'public-content map', ['version', 'siteRoutes', 'sourceLinks', 'pages']);
  if (map.version !== 1) throw new Error('Unsupported public-content map version');
  if (!Array.isArray(map.siteRoutes) || map.siteRoutes.length !== 2 ||
      !map.siteRoutes.includes('/') || !map.siteRoutes.includes('/download/')) {
    throw new Error('Public site routes must explicitly contain only / and /download/');
  }
  if (!map.sourceLinks || typeof map.sourceLinks !== 'object' || Array.isArray(map.sourceLinks)) {
    throw new Error('sourceLinks must explicitly map repository files to curation reasons');
  }
  for (const [source, reason] of Object.entries(map.sourceLinks)) {
    publicSourcePath(source);
    text(reason, `Curation reason for ${source}`);
  }
  if (!Array.isArray(map.pages) || !map.pages.length) throw new Error('Public pages must be an explicit list');
  const sources = new Set();
  const routes = new Set();
  const filenames = new Set();
  let hubs = 0;
  for (const page of map.pages) {
    record(page, 'public page', ['source', 'route', 'title', 'description', 'section', 'sidebar']);
    if (typeof page.route !== 'string' || !/^\/docs\/(?:[a-z0-9]+(?:-[a-z0-9]+)*\/)*$/.test(page.route)) {
      throw new Error(`Invalid public documentation route: ${page.route}`);
    }
    if (routes.has(page.route)) throw new Error(`Duplicate public route: ${page.route}`);
    routes.add(page.route);
    for (const key of ['title', 'description', 'section']) text(page[key], `Page ${key}`);
    record(page.sidebar, 'sidebar', ['label', 'order']);
    text(page.sidebar.label, 'Sidebar label');
    if (!Number.isSafeInteger(page.sidebar.order) || page.sidebar.order < 1) {
      throw new Error(`Sidebar order must be a positive integer: ${page.route}`);
    }
    if (page.source === null && page.route === '/docs/') {
      hubs++;
    } else {
      publicSourcePath(page.source);
      if (!page.source.startsWith('docs/') || !page.source.endsWith('.md') || page.route === '/docs/') {
        throw new Error(`Canonical public pages must be Markdown under docs/: ${page.source}`);
      }
      if (sources.has(page.source) || Object.hasOwn(map.sourceLinks, page.source)) {
        throw new Error(`Duplicate or ambiguous public source: ${page.source}`);
      }
      sources.add(page.source);
    }
  }
  if (hubs !== 1) throw new Error('The map must declare one generated /docs/ hub with source: null');
  for (const page of map.pages) {
    const filename = generatedFilename(page, map.pages);
    if (filenames.has(filename)) throw new Error(`Public routes collide at generated file: ${filename}`);
    filenames.add(filename);
  }
  return map;
}

function sourceURL(source) {
  return `${canonicalRepository}/blob/HEAD/${source.split('/').map(encodeURIComponent).join('/')}`;
}

function createLinkRewriter(map, repositoryRoot, publication) {
  const pages = new Map(map.pages.filter((page) => page.source).map((page) => [page.source, page.route]));
  const assets = new Map(publicAssets.map((asset) => [asset.source, `/${asset.destination}`]));
  const assetRoutes = new Map(publicAssets.map((asset) => [`/${asset.destination}`, asset.source]));
  const routes = new Set([...map.siteRoutes, ...map.pages.map((page) => page.route)]);
  const checked = new Map();
  const prefix = (route) => `${publication.base}${route.slice(1)}`;
  const imageURLs = new Set([...assetRoutes.keys()].map(prefix));
  const ensureFile = (source) => {
    if (!checked.has(source)) checked.set(source, repositoryFile(repositoryRoot, source));
    return checked.get(source);
  };

  async function sourceTarget(source, suffix) {
    publicSourcePath(source);
    await ensureFile(source);
    if (pages.has(source)) return `${prefix(pages.get(source))}${suffix}`;
    if (assets.has(source)) return `${prefix(assets.get(source))}${suffix}`;
    if (Object.hasOwn(map.sourceLinks, source)) return `${sourceURL(source)}${suffix}`;
    throw new Error(`Unknown public link target; curate it explicitly or remove the link: ${source}`);
  }

  async function knownSiteTarget(pathname, suffix) {
    const route = pathname.endsWith('/') ? pathname : `${pathname}/`;
    if (routes.has(route)) return `${prefix(route)}${suffix}`;
    if (assetRoutes.has(pathname)) {
      await ensureFile(assetRoutes.get(pathname));
      return `${prefix(pathname)}${suffix}`;
    }
    const source = pathname.slice(1).replace(/\/$/, '');
    if (pages.has(source) || assets.has(source) || Object.hasOwn(map.sourceLinks, source)) {
      return sourceTarget(source, suffix);
    }
  }

  async function siteTarget(pathname, suffix) {
    const canonical = await knownSiteTarget(pathname, suffix);
    if (canonical !== undefined) return canonical;
    if (publication.base !== '/' && (pathname === publication.base.slice(0, -1) ||
        pathname.startsWith(publication.base))) {
      pathname = `/${pathname.slice(publication.base.length).replace(/^\/+/, '')}`;
      const prefixed = await knownSiteTarget(pathname, suffix);
      if (prefixed !== undefined) return prefixed;
    }
    publicSourcePath(pathname.slice(1).replace(/\/$/, ''));
    throw new Error(`Unknown public route or asset: ${pathname}`);
  }

  async function rewriteURL(value, page) {
    if (!value || value.startsWith('#')) return value;
    if (/[\u0000-\u0020\u007f\\]/u.test(value) || value.startsWith('//')) {
      throw new Error(`Ambiguous or unsafe link: ${value}`);
    }
    if (/^[a-z][a-z0-9+.-]*:/i.test(value)) {
      const url = new URL(value);
      if (!['https:', 'http:', 'mailto:'].includes(url.protocol) || url.username || url.password) {
        throw new Error(`Unsupported or credential-bearing link: ${value}`);
      }
      if (url.protocol === 'mailto:') return value;
      if (url.origin === 'https://sodapop.sh' || url.origin === publication.origin) {
        return siteTarget(decodeURIComponent(url.pathname), `${url.search}${url.hash}`);
      }
      const sourceMatch = url.origin === 'https://github.com' &&
        /^\/VeVarunSharma\/sodapop\/(?:blob|tree)\/[^/]+\/(.+)$/i.exec(url.pathname);
      if (sourceMatch) return sourceTarget(decodeURIComponent(sourceMatch[1]), `${url.search}${url.hash}`);
      return value;
    }

    const boundary = value.search(/[?#]/);
    const pathname = decodeURIComponent(boundary === -1 ? value : value.slice(0, boundary));
    const suffix = boundary === -1 ? '' : value.slice(boundary);
    if (/[\\\u0000-\u001f\u007f]/u.test(pathname)) throw new Error(`Unsafe link path: ${value}`);
    if (!pathname) return `${prefix(page.route)}${suffix}`;
    if (pathname.startsWith('/')) return siteTarget(pathname, suffix);
    const resolved = path.resolve(repositoryRoot, path.posix.dirname(page.source), pathname);
    const relative = path.relative(path.resolve(repositoryRoot), resolved);
    if (relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
      throw new Error(`Link target is outside the repository: ${value}`);
    }
    return sourceTarget(relative.split(path.sep).join('/'), suffix);
  }

  return async (value, page, image = false) => {
    const rewritten = await rewriteURL(value, page);
    if (image && !imageURLs.has(rewritten.split(/[?#]/, 1)[0])) {
      throw new Error(`Images must reference an explicitly approved public asset: ${value}`);
    }
    return rewritten;
  };
}

function hubContent(map, base) {
  const tree = {
    type: 'root',
    children: [{
      type: 'paragraph',
      children: [
        { type: 'text', value: "First sip? Let's get Sodapop running in your project. Check the " },
        { type: 'link', url: `${base}download/`, children: [{ type: 'text', value: 'download page' }] },
        { type: 'text', value: ' for current release and installation availability, then choose a guide below.' },
      ],
    }],
  };
  const sections = new Map();
  for (const page of map.pages.filter((entry) => entry.source)) {
    if (!sections.has(page.section)) sections.set(page.section, []);
    sections.get(page.section).push(page);
  }
  for (const [section, pages] of sections) {
    tree.children.push(
      { type: 'heading', depth: 2, children: [{ type: 'text', value: section }] },
      {
        type: 'list', ordered: false, spread: false,
        children: [...pages].sort((a, b) => a.sidebar.order - b.sidebar.order ||
          (a.route < b.route ? -1 : a.route > b.route ? 1 : 0)).map((page) => ({
          type: 'listItem', spread: false, children: [{
            type: 'paragraph', children: [
              { type: 'link', url: `${base}${page.route.slice(1)}`, children: [{ type: 'text', value: page.title }] },
              { type: 'text', value: `: ${page.description}` },
            ],
          }],
        })),
      },
    );
  }
  return markdown.stringify(tree);
}

function stringifyLink(node) {
  const child = node.type === 'definition' ? node : { type: 'paragraph', children: [node] };
  return markdown.stringify({ type: 'root', children: [child] }).replace(/\n$/, '');
}

async function adaptDocument(source, page, rewriteURL, installationReference) {
  const markers = source.split(installationMarker).length - 1;
  if (page.route === '/docs/installation/') {
    if (markers !== 1) throw new Error(`${page.source} must contain exactly one ${installationMarker} hook`);
    if (installationReference !== undefined) source = source.replace(installationMarker, () => installationReference);
  } else if (markers) {
    throw new Error(`Installation catalog hook is only allowed on the installation page: ${page.source}`);
  }

  const tree = markdown.parse(source);
  const title = tree.children[0];
  const headings = tree.children.filter((node) => node.type === 'heading' && node.depth === 1);
  if (title?.type !== 'heading' || title.depth !== 1 || headings.length !== 1) {
    throw new Error(`${page.source} must start with exactly one level-one heading, without frontmatter`);
  }
  const links = [];
  const imageReferences = new Set();
  visit(tree, (node) => {
    if (node.type === 'html' && node.value.trim() !== installationMarker) {
      throw new Error(`${page.source}:${node.position.start.line}: Raw HTML is not allowed in public Markdown; use Markdown links and images`);
    }
    if (['link', 'image', 'definition'].includes(node.type)) links.push(node);
    if (node.type === 'imageReference') imageReferences.add(node.identifier);
  });
  const changed = [];
  for (const node of links) {
    try {
      const rewritten = await rewriteURL(node.url, page,
        node.type === 'image' || (node.type === 'definition' && imageReferences.has(node.identifier)));
      if (rewritten !== node.url) {
        node.url = rewritten;
        changed.push(node);
      }
    } catch (error) {
      throw new Error(`${page.source}:${node.position.start.line}: ${error.message}`, { cause: error });
    }
  }

  // Patch complete parsed link nodes, not the whole document: GFM tables and
  // code examples keep their original spelling without a second parser dialect.
  const patches = [{
    start: title.position.start.offset, end: title.position.end.offset, value: '',
  }];
  for (const node of changed.sort((a, b) => a.position.start.offset - b.position.start.offset ||
    b.position.end.offset - a.position.end.offset)) {
    const start = node.position.start.offset;
    const end = node.position.end.offset;
    if (patches.some((patch) => patch.start <= start && patch.end >= end)) continue;
    patches.push({ start, end, value: stringifyLink(node) });
  }
  for (const patch of patches.sort((a, b) => b.start - a.start)) {
    source = source.slice(0, patch.start) + patch.value + source.slice(patch.end);
  }
  const body = source.replace(/^(?:[ \t]*\r?\n)+/, '');
  return body.endsWith('\n') ? body : `${body}\n`;
}

function frontmatter(page) {
  return [
    '---',
    `title: ${JSON.stringify(page.title)}`,
    `description: ${JSON.stringify(page.description)}`,
    'sidebar:',
    `  label: ${JSON.stringify(page.sidebar.label)}`,
    `  order: ${page.sidebar.order}`,
    'editUrl: false',
    '---',
    '',
  ].join('\n');
}

/**
 * Adapt the explicit canonical Markdown map into the owned /docs/ staging tree.
 * @param {{repositoryRoot?: string, siteRoot?: string, base?: string, installationReference?: string}} options
 * @returns {Promise<{base: string, outputDirectory: string, pages: Array<{source: string|null, route: string, url: string, file: string}>}>}
 */
export async function prepareContent(options = {}) {
  const repositoryRoot = options.repositoryRoot ?? defaultRepositoryRoot;
  const siteRoot = options.siteRoot ?? defaultSiteRoot;
  const publication = siteConfiguration();
  publication.base = normalizeBase(options.base ?? publication.base);
  const { installationReference } = options;
  if (installationReference !== undefined && (typeof installationReference !== 'string' ||
      installationReference.includes(installationMarker))) {
    throw new Error('installationReference must be Markdown without another installation catalog hook');
  }
  const map = validateMap(JSON.parse((await readRepositoryFile(siteRoot, mapPath)).toString('utf8')));
  if (installationReference !== undefined && !map.pages.some((page) => page.route === '/docs/installation/')) {
    throw new Error('An installation reference was supplied without a mapped installation page');
  }
  for (const source of Object.keys(map.sourceLinks)) await repositoryFile(repositoryRoot, source);
  const rewriteURL = createLinkRewriter(map, repositoryRoot, publication);
  const files = new Map();
  const pages = [];
  for (const page of map.pages) {
    const file = generatedFilename(page, map.pages);
    const body = page.source
      ? await adaptDocument((await readRepositoryFile(repositoryRoot, page.source)).toString('utf8'),
        page, rewriteURL, installationReference)
      : hubContent(map, publication.base);
    files.set(file, `${frontmatter(page)}${body}`);
    pages.push({ source: page.source, route: page.route, url: `${publication.base}${page.route.slice(1)}`, file });
  }
  await replaceOwnedDirectory(siteRoot, outputDirectory, files);
  return { base: publication.base, outputDirectory: path.resolve(siteRoot, outputDirectory), pages };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = await prepareContent();
  console.info(`Prepared ${result.pages.length} public documentation pages.`);
}
