#!/usr/bin/env node
/**
 * Explicit operator refresh: npm --prefix site run release:sync.
 *
 * Configure only src/data/channels.json:
 * - mode: "pre-release" (local, no write/network) or "release".
 * - repository: the canonical VeVarunSharma/sodapop identity, never an endpoint.
 * - releaseTag: null for public latest stable, or an exact stable vX.Y.Z.
 * - channels.npm.verify: opt in to checking @sodapop-sh/cli and every package declared
 *   by its published sodapop manifest, including Windows x64 when declared.
 * - channels.homebrew.verify: opt in after setting repository to the confirmed
 *   public VeVarunSharma/homebrew-sodapop tap; formula must remain "sodapop".
 *
 * SODAPOP_SITE_PREVIEW=1 explicitly returns an empty in-memory preview catalog,
 * without reading published metadata or making requests/writes. Unset or "0"
 * retains strict production behavior; other values are errors.
 *
 * No credentials, endpoint overrides, fixture flags, publishing, subprocesses,
 * or native/tarball downloads. Metadata is anonymous, bounded, and fail-closed.
 * Source templates establish identities, not public installation availability.
 * Metadata/hash agreement is not publisher authentication, signing, attestation
 * verification, or native installation qualification. Those are separate gates.
 */
import { createHash, randomUUID } from 'node:crypto';
import { mkdir, open, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  catalogFromManifest, catalogPaths, installCommands, readCatalog, readReleaseInputs,
  isDependencyVersion, npmTarget, parseReleaseJSON, previewCatalog, releaseIdentities, stableVersion, validateCatalog,
} from './release-catalog.mjs';

/** @typedef {import('../src/lib/catalog-types.ts').PublishedReleaseCatalog} PublishedReleaseCatalog */
/** @typedef {import('../src/lib/catalog-types.ts').ReleaseConfiguration} ReleaseConfiguration */
/**
 * Injected release sources/requests/writes are marked as fixtures and require a
 * test output path. Those injection options are not available through the CLI
 * or environment; the explicit preview flag never loads fixture metadata.
 * @typedef {import('./release-catalog.mjs').CatalogOptions & {
 *   fetch?: typeof globalThis.fetch,
 *   timeoutMs?: number,
 *   replaceFile?: typeof rename
 * }} ResolveOptions
 */

const api = 'https://api.github.com';
const registry = 'https://registry.npmjs.org';
const jsonLimit = 256 * 1024;
const manifestLimit = 64 * 1024;
const sidecarLimit = 512;
const maxRequests = 32;
const refreshLimitMs = 120_000;

class LookupError extends Error {}

function fail(message) {
  throw new LookupError(message);
}

function record(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be a JSON object`);
  return value;
}

function json(bytes, label) {
  try {
    return parseReleaseJSON(new TextDecoder('utf-8', { fatal: true }).decode(bytes), label);
  } catch (error) {
    if (!(error instanceof SyntaxError) && !(error instanceof TypeError)) throw error;
    fail(`${label} is not valid UTF-8 JSON`);
  }
}

function digest(bytes, algorithm = 'sha256') {
  return createHash(algorithm).update(bytes).digest('hex');
}

function hash(value, length = 64) {
  return typeof value === 'string' && value.length === length && /^[0-9a-f]+$/.test(value);
}

function publishedTimestamp(value) {
  if (typeof value !== 'string' || ![20, 24].includes(value.length) ||
      !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{3})?Z$/.test(value) ||
      !Number.isFinite(Date.parse(value))) return false;
  return new Date(value).toISOString() === (value.length === 20 ? value.replace('Z', '.000Z') : value);
}

function safeURL(value) {
  if (typeof value !== 'string' || value.length > 8192 || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  if (!URL.canParse(value)) return false;
  const url = new URL(value);
  return url.protocol === 'https:' && !url.username && !url.password && !url.port && !url.hash &&
    url.href === value;
}

function assetRedirect(value) {
  if (!safeURL(value)) return false;
  const url = new URL(value);
  return ['release-assets.githubusercontent.com', 'objects.githubusercontent.com'].includes(url.hostname) &&
    /^\/github-production-release-asset(?:-[a-z0-9]+)?\/[A-Za-z0-9/_.-]+$/.test(url.pathname) &&
    !url.pathname.includes('/../');
}

async function bodyBytes(response, limit, label) {
  const length = response.headers.get('content-length');
  if (length !== null && (!/^\d+$/.test(length) || !Number.isSafeInteger(Number(length)) || Number(length) > limit)) {
    fail(`${label} exceeds its ${limit}-byte response limit`);
  }
  if (!response.body) fail(`${label} returned an empty response body`);
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) fail(`${label} exceeds its ${limit}-byte response limit`);
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
  return Buffer.concat(chunks, size);
}

function requester(fetchImplementation, timeoutMs) {
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 30_000) {
    throw new Error('Request timeoutMs must be between 1 and 30000');
  }
  let requests = 0;
  const deadline = Date.now() + refreshLimitMs;
  return async (initialURL, label, limit = jsonLimit, { missing = null, asset = false } = {}) => {
    if (!safeURL(initialURL) || ![api, registry, 'https://github.com'].includes(new URL(initialURL).origin) ||
        new URL(initialURL).search) fail(`${label} has an unsafe request URL`);
    const controller = new AbortController();
    const wait = Math.min(timeoutMs, deadline - Date.now());
    if (wait <= 0) fail('Release refresh exceeded its total request deadline');
    let timer;
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => {
        controller.abort();
        reject(new LookupError(`${label} request timed out; check public access and retry release:sync`));
      }, wait);
    });
    const work = async () => {
      let url = initialURL;
      for (let redirects = 0; redirects <= 3; redirects += 1) {
        requests += 1;
        if (requests > maxRequests) fail('Release refresh exceeded its bounded request budget');
        const isAPI = new URL(url).origin === api;
        const response = await fetchImplementation(url, {
          method: 'GET', redirect: 'manual', credentials: 'omit', signal: controller.signal,
          headers: {
            accept: isAPI ? 'application/vnd.github+json' : asset ? 'application/octet-stream' : 'application/json',
            ...(isAPI ? { 'X-GitHub-Api-Version': '2022-11-28' } : {}),
          },
        });
        if (response.redirected || (response.url && response.url !== url)) {
          fail(`${label} followed an unvalidated redirect`);
        }
        if ([301, 302, 303, 307, 308].includes(response.status)) {
          const location = response.headers.get('location');
          if (!asset || !assetRedirect(location)) fail(`${label} returned an unsafe redirect`);
          url = location;
          continue;
        }
        if (response.status === 404 && missing) {
          const absent = record(json(await bodyBytes(response, 8192, label), label), label);
          if ((missing === 'npm' && ['Not found', 'Not Found'].includes(absent.error)) ||
              (missing === 'github' && absent.message === 'Not Found')) return null;
          fail(`${label} returned an ambiguous 404; refusing to infer publication state`);
        }
        if (response.status !== 200) {
          fail(`${label} returned HTTP ${response.status}; confirm the configured resource is public and retry release:sync`);
        }
        return bodyBytes(response, limit, label);
      }
      fail(`${label} exceeded the redirect limit`);
    };
    try {
      return await Promise.race([work(), timeout]);
    } catch (error) {
      if (error instanceof LookupError) throw error;
      // Fetch/stream errors can include signed URLs or credentials. Do not echo them.
      throw new LookupError(`${label} request failed; check public access/network connectivity and retry release:sync`);
    } finally {
      clearTimeout(timer);
      controller.abort();
    }
  };
}

async function publicRepository(request, repository, label) {
  const value = record(json(await request(`${api}/repos/${repository}`, label), label), label);
  if (value.full_name !== repository || value.private !== false ||
      (value.visibility !== undefined && value.visibility !== 'public') ||
      value.html_url !== `https://github.com/${repository}` ||
      value.url !== `${api}/repos/${repository}`) {
    fail(`${label} must be the configured public repository, not a private, moved, or different repository`);
  }
  return value;
}

function releaseMetadata(value, configuration) {
  record(value, 'GitHub release');
  if (!Number.isSafeInteger(value.id) || value.id < 1 || value.draft !== false || value.prerelease !== false ||
      typeof value.tag_name !== 'string' || !value.tag_name.startsWith('v')) {
    fail('GitHub release must be a public stable, non-draft release with an exact vX.Y.Z tag');
  }
  const version = stableVersion(value.tag_name.slice(1), 'Release tag');
  const releaseURL = `https://github.com/${configuration.repository}/releases/tag/v${version}`;
  if (value.html_url !== releaseURL || value.url !== `${api}/repos/${configuration.repository}/releases/${value.id}` ||
      value.assets_url !== `${api}/repos/${configuration.repository}/releases/${value.id}/assets` ||
      !publishedTimestamp(value.published_at) ||
      (configuration.releaseTag !== null && value.tag_name !== configuration.releaseTag)) {
    fail('GitHub release URL, publication metadata, or configured tag is invalid');
  }
  if (!Array.isArray(value.assets) || value.assets.length !== 11) {
    fail('Public release must contain exactly five archives, five sidecars, and one manifest');
  }
  const assets = new Map();
  const ids = new Set();
  const base = `https://github.com/${configuration.repository}/releases/download/v${version}`;
  for (const asset of value.assets) {
    record(asset, 'GitHub release asset');
    if (typeof asset.name !== 'string' || asset.name.length > 255 || asset.name.trim() !== asset.name ||
        !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(asset.name) || assets.has(asset.name) ||
        !Number.isSafeInteger(asset.id) || asset.id < 1 || ids.has(asset.id) ||
        asset.state !== 'uploaded' || !Number.isSafeInteger(asset.size) || asset.size < 1 ||
        asset.size > 4 * 1024 ** 3 || asset.browser_download_url !== `${base}/${asset.name}` ||
        asset.url !== `${api}/repos/${configuration.repository}/releases/assets/${asset.id}`) {
      fail('GitHub release has duplicate, incomplete, or unsafe asset metadata/URLs');
    }
    if (asset.digest !== null && asset.digest !== undefined &&
        (typeof asset.digest !== 'string' || !asset.digest.startsWith('sha256:') || !hash(asset.digest.slice(7)))) {
      fail('GitHub release asset digest must be a SHA-256 digest when supplied');
    }
    assets.set(asset.name, asset);
    ids.add(asset.id);
  }
  return { version, assets };
}

async function tagCommit(request, repository, tag) {
  const base = `${api}/repos/${repository}/git`;
  let value = record(json(await request(`${base}/ref/tags/${tag}`, 'Release Git tag'), 'Release Git tag'), 'Release Git tag');
  if (value.ref !== `refs/tags/${tag}`) fail('Release Git tag does not match the selected release');
  const seen = new Set();
  for (let depth = 0; depth < 4; depth += 1) {
    const target = record(value.object, 'Release Git tag target');
    if (!hash(target.sha, 40) || !['commit', 'tag'].includes(target.type) || seen.has(target.sha)) {
      fail('Release Git tag has an invalid or cyclic target');
    }
    const targetURL = `${base}/${target.type === 'tag' ? 'tags' : 'commits'}/${target.sha}`;
    if (target.url !== targetURL) fail('Release Git tag has an unsafe target URL');
    if (target.type === 'commit') return target.sha;
    seen.add(target.sha);
    value = record(json(await request(targetURL, 'Annotated release tag'), 'Annotated release tag'), 'Annotated release tag');
    if (value.sha !== target.sha) fail('Annotated release tag identity does not match its target');
  }
  fail('Release Git tag exceeded the bounded annotated-tag depth');
}

async function smallAsset(request, asset, label, limit) {
  if (!asset || asset.size > limit) fail(`${label} is missing or exceeds its metadata size limit`);
  const bytes = await request(asset.browser_download_url, label, limit, { asset: true });
  if (bytes.length !== asset.size || (asset.digest != null && asset.digest !== `sha256:${digest(bytes)}`)) {
    fail(`${label} bytes do not match GitHub asset size/digest metadata`);
  }
  return bytes;
}

async function nativeRelease(request, configuration) {
  await publicRepository(request, configuration.repository, 'Canonical release repository');
  const selector = configuration.releaseTag === null ? 'latest' : `tags/${configuration.releaseTag}`;
  const release = json(await request(`${api}/repos/${configuration.repository}/releases/${selector}`, 'Public stable release'), 'Public stable release');
  const { version, assets } = releaseMetadata(release, configuration);
  const manifestName = `sodapop-${version}-manifest.json`;
  const bytes = await smallAsset(request, assets.get(manifestName), 'Published release manifest', manifestLimit);
  const catalog = catalogFromManifest(json(bytes, 'Published release manifest'), configuration);
  if (catalog.version !== version || catalog.releaseUrl !== release.html_url ||
      catalog.manifestUrl !== assets.get(manifestName).browser_download_url) {
    fail('Published manifest version/URLs do not match the selected release');
  }
  if (catalog.commit !== await tagCommit(request, configuration.repository, `v${version}`)) {
    fail('Published manifest commit does not match the selected Git release tag');
  }
  const expected = new Set([manifestName]);
  for (const artifact of catalog.artifacts) {
    expected.add(artifact.archive);
    expected.add(`${artifact.archive}.sha256`);
    const archive = assets.get(artifact.archive);
    if (!archive || archive.browser_download_url !== artifact.archiveUrl ||
        (archive.digest != null && archive.digest !== `sha256:${artifact.archiveSha256}`)) {
      fail(`Published archive metadata does not match the manifest for ${artifact.platform}`);
    }
    const checksum = await smallAsset(request, assets.get(`${artifact.archive}.sha256`),
      `Published ${artifact.platform} checksum`, sidecarLimit);
    const line = new TextDecoder('utf-8', { fatal: true }).decode(checksum).replace(/\r?\n$/, '');
    const entry = /^([0-9a-f]{64})[ \t]+([A-Za-z0-9._-]+)$/.exec(line);
    if (/[\r\n]/.test(line) || !entry || entry[1] !== artifact.archiveSha256 || entry[2] !== artifact.archive) {
      fail(`Published checksum does not match the manifest archive name/hash for ${artifact.platform}`);
    }
  }
  if ([...assets.keys()].some((name) => !expected.has(name))) fail('Public release contains unexpected native assets');
  return catalog;
}

function packageIdentity(value, name) {
  record(value, 'npm package');
  const version = stableVersion(value.version, 'npm package version');
  if (value.name !== name || value.private === true || value.deprecated ||
      value.repository?.url !== `git+https://github.com/${releaseIdentities.repository}.git`) {
    fail('Published npm package identity does not match the owned source package');
  }
  const basename = name.slice(name.indexOf('/') + 1);
  if (value.dist?.tarball !== `${registry}/${name}/-/${basename}-${version}.tgz` ||
      typeof value.dist.integrity !== 'string' || !/^sha512-[A-Za-z0-9+/]{86}==$/.test(value.dist.integrity) ||
      value.dist.integrity.length !== 95 ||
      Buffer.from(value.dist.integrity.slice(7), 'base64').toString('base64') !== value.dist.integrity.slice(7)) {
    fail('Published npm package has an invalid registry tarball URL or SHA-512 integrity');
  }
  return version;
}

function equalObject(value, expected) {
  return value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === Object.keys(expected).length &&
    Object.entries(expected).every(([key, entry]) => Object.hasOwn(value, key) && value[key] === entry);
}

function packageBinding(value, catalog, artifacts, plural) {
  const header = {
    schema_version: 1, version: catalog.version, commit: catalog.commit,
    copilot_sdk_version: catalog.copilotSdkVersion, copilot_runtime_version: catalog.copilotRuntimeVersion,
  };
  const key = plural ? 'artifacts' : 'artifact';
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      Object.keys(value).length !== Object.keys(header).length + 1 ||
      Object.entries(header).some(([key, expected]) => !Object.hasOwn(value, key) || value[key] !== expected) ||
      !Object.hasOwn(value, key)) {
    fail('Published npm sodapop manifest header does not match the native release');
  }
  const declared = plural ? value.artifacts : [value.artifact];
  const seen = new Set();
  if (!Array.isArray(declared) || declared.length !== artifacts.length) fail('Published npm manifest platform set is incomplete');
  for (const entry of declared) {
    const expected = artifacts.find(({ platform }) => platform === entry?.platform);
    if (!expected || seen.has(entry.platform) || !equalObject(entry, {
      platform: expected.platform, archive: expected.archive,
      archive_sha256: expected.archiveSha256, binary_sha256: expected.binarySha256,
    })) fail('Published npm sodapop artifact metadata differs from the native manifest');
    seen.add(entry.platform);
  }
}

async function npmChannel(request, catalog, nodeRequirement) {
  const bytes = await request(`${registry}/@sodapop-sh%2fcli/latest`, 'Public npm launcher', jsonLimit, { missing: 'npm' });
  if (bytes === null) return;
  const launcher = json(bytes, 'Public npm launcher');
  const version = packageIdentity(launcher, releaseIdentities.npmPackage);
  const declared = launcher.sodapop?.artifacts;
  if (!Array.isArray(declared) || declared.length === 0 || declared.length > catalog.artifacts.length) {
    fail('Published npm manifest must declare a nonempty supported platform set');
  }
  const artifacts = catalog.artifacts.filter(({ platform }) => declared.some((entry) => entry?.platform === platform));
  if (artifacts.length !== declared.length) fail('Published npm manifest has duplicate or unsupported platforms');
  const dependencies = Object.fromEntries(artifacts.map(({ platform }) => [npmTarget(platform).name, version]));
  if (!equalObject(launcher.bin, { sodapop: 'bin/sodapop.js' }) ||
      !equalObject(launcher.optionalDependencies, dependencies) || launcher.engines?.node !== nodeRequirement) {
    fail('Published npm launcher must match the source command, Node requirement, and exact manifest-declared optionalDependencies');
  }
  if (version !== catalog.version) return;
  packageBinding(launcher.sodapop, catalog, artifacts, true);
  for (const artifact of artifacts) {
    const { name, os, cpu } = npmTarget(artifact.platform);
    const bytes = await request(`${registry}/${name.replace('/', '%2f')}/${version}`, 'Public native npm package',
      jsonLimit, { missing: 'npm' });
    if (bytes === null) return;
    const pkg = json(bytes, 'Public native npm package');
    if (packageIdentity(pkg, name) !== version || JSON.stringify(pkg.os) !== JSON.stringify([os]) ||
        JSON.stringify(pkg.cpu) !== JSON.stringify([cpu]) ||
        (os === 'linux' && JSON.stringify(pkg.libc) !== '["glibc"]')) {
      fail('Published native npm platform/version/libc differs from the source contract');
    }
    packageBinding(pkg.sodapop, catalog, [artifact], false);
  }
  catalog.channels.npm = {
    status: 'published', command: installCommands.npm, version,
    url: 'https://www.npmjs.com/package/@sodapop-sh/cli', platforms: artifacts.map(({ platform }) => platform),
  };
}

async function homebrewChannel(request, catalog, template) {
  const repository = releaseIdentities.homebrewRepository;
  const repo = await publicRepository(request, repository, 'Configured public Homebrew tap');
  if (typeof repo.default_branch !== 'string' || repo.default_branch.length > 200 ||
      !/^[A-Za-z0-9][A-Za-z0-9/_.-]*$/.test(repo.default_branch) ||
      repo.default_branch.split('/').some((segment) => ['', '.', '..'].includes(segment))) {
    fail('Homebrew tap must have a valid public default branch');
  }
  const bytes = await request(`${api}/repos/${repository}/contents/Formula/sodapop.rb`, 'Published Homebrew formula',
    jsonLimit, { missing: 'github' });
  if (bytes === null) return;
  const file = record(json(bytes, 'Published Homebrew formula'), 'Published Homebrew formula');
  if (file.type !== 'file' || file.path !== 'Formula/sodapop.rb' || file.encoding !== 'base64' ||
      !hash(file.sha, 40) || typeof file.content !== 'string' ||
      file.html_url !== `https://github.com/${repository}/blob/${repo.default_branch}/Formula/sodapop.rb` ||
      file.download_url !== `https://raw.githubusercontent.com/${repository}/${repo.default_branch}/Formula/sodapop.rb`) {
    fail('Published Homebrew formula must be the owned formula on the public default branch');
  }
  const encoded = file.content.replaceAll('\n', '');
  const content = Buffer.from(encoded, 'base64');
  if (content.toString('base64') !== encoded || content.length > manifestLimit ||
      file.size !== content.length || file.sha !== digest(Buffer.concat([
        Buffer.from(`blob ${content.length}\0`), content,
      ]), 'sha1')) {
    fail('Published Homebrew formula has inconsistent content/size/Git blob metadata');
  }
  const formula = new TextDecoder('utf-8', { fatal: true }).decode(content).replaceAll('\r\n', '\n');
  const versions = [...formula.matchAll(/^  version "([^"\r\n]+)"$/gm)];
  const pins = [...formula.matchAll(/^      Copilot SDK ([^\s]+) \/ runtime ([^\s]+)$/gm)];
  const hashes = [...formula.matchAll(/^      sha256 "([0-9a-f]{64})"$/gm)];
  if (versions.length !== 1 || pins.length !== 1 || hashes.length !== 4 ||
      !isDependencyVersion(pins[0][1]) || !isDependencyVersion(pins[0][2])) {
    fail('Published Homebrew formula has invalid version/hash declarations');
  }
  const version = stableVersion(versions[0][1], 'Homebrew version');
  let index = 0;
  const declared = template.replaceAll('@VERSION@', version)
    .replaceAll('@COPILOT_SDK_VERSION@', pins[0][1]).replaceAll('@COPILOT_RUNTIME_VERSION@', pins[0][2])
    .replace(/@[A-Z0-9_]+_SHA256@/g, () => hashes[index++][1]);
  if (formula !== declared) fail('Published Homebrew formula differs from the approved generated source template');
  if (version !== catalog.version) return;
  let expected = template.replaceAll('@VERSION@', version)
    .replaceAll('@COPILOT_SDK_VERSION@', catalog.copilotSdkVersion)
    .replaceAll('@COPILOT_RUNTIME_VERSION@', catalog.copilotRuntimeVersion);
  for (const artifact of catalog.artifacts.filter(({ platform }) => !platform.startsWith('windows/'))) {
    const placeholder = `@${artifact.platform.replace('/', '_').toUpperCase()}_SHA256@`;
    expected = expected.replaceAll(placeholder, artifact.archiveSha256);
  }
  if (formula !== expected) fail('Published Homebrew formula hashes or SDK/runtime versions do not match the release manifest');
  catalog.channels.homebrew = {
    status: 'published', command: installCommands.homebrew, version,
    url: `https://github.com/${repository}`,
    platforms: catalog.artifacts.filter(({ platform }) => !platform.startsWith('windows/')).map(({ platform }) => platform),
  };
}

async function writeSnapshot(filename, value, replaceFile) {
  await mkdir(path.dirname(filename), { recursive: true });
  const temporary = path.join(path.dirname(filename), `.releases-${randomUUID()}.tmp`);
  let file;
  try {
    file = await open(temporary, 'wx', 0o600);
    await file.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await file.sync();
    await file.close();
    file = undefined;
    await replaceFile(temporary, filename);
  } finally {
    if (file) await file.close();
    await rm(temporary, { force: true });
  }
}

/**
 * Refresh explicit public release metadata and atomically return/persist the
 * normalized catalog. A missing channel is not a missing native release.
 * Explicit preview mode returns preview:true without requests, writes, or
 * published-metadata reads; it cannot replace the production release snapshot.
 * @param {ResolveOptions} [options]
 * @returns {Promise<import('../src/lib/catalog-types.ts').ReleaseCatalog>}
 */
export async function resolveReleases(options = {}) {
  const preview = previewCatalog(options.environment ?? process.env);
  if (preview) return preview;
  const { configuration, homebrewTemplate, npmNodeRequirement } = await readReleaseInputs(options);
  if (configuration.mode === 'pre-release') return readCatalog(options);
  const injected = ['fetch', 'configPath', 'resolvedPath', 'repositoryRoot', 'timeoutMs', 'replaceFile']
    .some((key) => Object.hasOwn(options, key));
  const filename = options.resolvedPath ?? catalogPaths.resolved;
  if (injected && path.resolve(filename) === path.resolve(catalogPaths.resolved)) {
    throw new Error('Injected release fixtures require a nonproduction resolvedPath; refusing to overwrite public metadata');
  }
  const request = requester(options.fetch ?? globalThis.fetch, options.timeoutMs ?? 10_000);
  const catalog = await nativeRelease(request, configuration);
  if (configuration.channels.npm.verify) await npmChannel(request, catalog, npmNodeRequirement);
  if (configuration.channels.homebrew.verify) await homebrewChannel(request, catalog, homebrewTemplate);
  const normalized = validateCatalog(catalog, configuration);
  await writeSnapshot(filename, {
    schemaVersion: 1, source: injected ? 'fixture' : 'public',
    repository: configuration.repository, checkedAt: new Date().toISOString(), catalog: normalized,
  }, options.replaceFile ?? rename);
  return normalized;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.slice(2);
    if (args.length === 1 && args[0] === '--help') {
      console.log('Usage: npm --prefix site run release:sync\nEdit src/data/channels.json to select explicit pre-release/release mode, an optional stable releaseTag, and independent channel verify flags. SODAPOP_SITE_PREVIEW=1 selects an offline preview without release metadata reads, requests, or writes; unset or 0 retains strict production behavior. No tokens, endpoint overrides, or fixture flags are accepted.');
    } else {
      if (args.length !== 0) throw new Error('Unsupported arguments; use --help for the operator-controlled channel configuration');
      const catalog = await resolveReleases();
      console.log(catalog.mode === 'pre-release'
        ? catalog.preview
          ? 'Sodapop site: explicit offline preview; no network requests or release metadata writes.'
          : 'Sodapop site: explicit pre-release mode; no network requests or release metadata writes.'
        : `Sodapop site: saved ${catalog.version} (${catalog.artifacts.length} native platforms; npm ${catalog.channels.npm.status}; Homebrew ${catalog.channels.homebrew.status}).`);
    }
  } catch (error) {
    console.error(`Sodapop site release refresh: ${error instanceof Error ? error.message : 'Unknown failure'}`);
    process.exitCode = 1;
  }
}
