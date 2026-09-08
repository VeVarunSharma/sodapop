/**
 * Server/build-only, offline catalog reader. Public types live in catalog-types.ts.
 * channels.json is explicit operator configuration, not evidence of publication.
 * Only resolve-releases.mjs writes .generated/releases.json. Its envelope records
 * public vs injected-fixture provenance; normal builds never accept fixtures.
 * SODAPOP_SITE_PREVIEW=1 returns an empty in-memory catalog before metadata I/O.
 */
import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import path from 'node:path';
import { isPreview, siteRoot, repositoryRoot as defaultRepositoryRoot } from './site-config.mjs';

/** @typedef {import('../src/lib/catalog-types.ts').ReleaseCatalog} ReleaseCatalog */
/** @typedef {import('../src/lib/catalog-types.ts').PublishedReleaseCatalog} PublishedReleaseCatalog */
/** @typedef {import('../src/lib/catalog-types.ts').ReleaseConfiguration} ReleaseConfiguration */
/**
 * Paths are dependency-injection seams for credential-free tests, not CLI inputs.
 * @typedef {object} CatalogOptions
 * @property {string} [configPath]
 * @property {string} [resolvedPath]
 * @property {string} [repositoryRoot]
 * @property {boolean} [allowFixtures] Requires a nonproduction resolvedPath.
 * @property {NodeJS.ProcessEnv} [environment] Defaults to process.env; preview accepts only "0"/"1".
 */

export const releaseIdentities = Object.freeze({
  repository: 'VeVarunSharma/sodapop',
  npmPackage: '@sodapop-sh/cli',
  homebrewRepository: 'VeVarunSharma/homebrew-sodapop',
  homebrewFormula: 'sodapop',
});

export const installCommands = Object.freeze({
  npm: 'npm install --global @sodapop-sh/cli',
  homebrew: 'brew install VeVarunSharma/sodapop/sodapop',
});

export const nativePlatforms = Object.freeze(/** @type {const} */ ([
  { platform: 'darwin/arm64', label: 'macOS (Apple silicon)' },
  { platform: 'darwin/amd64', label: 'macOS (Intel x64)' },
  { platform: 'linux/arm64', label: 'Linux (ARM64)' },
  { platform: 'linux/amd64', label: 'Linux (x64)' },
  { platform: 'windows/amd64', label: 'Windows (x64)' },
]));

/**
 * @internal Source package identities are capabilities, not publication evidence.
 * @param {import('../src/lib/catalog-types.ts').NativePlatform} platform
 */
export function npmTarget(platform) {
  if (!nativePlatforms.some((target) => target.platform === platform)) throw new Error('Unsupported npm platform');
  const [os, arch] = platform.split('/');
  return {
    name: `@sodapop-sh/${platform.replace('/', '-')}`,
    os: os === 'windows' ? 'win32' : os,
    cpu: arch === 'amd64' ? 'x64' : arch,
    binary: os === 'windows' ? 'sodapop.exe' : 'sodapop',
  };
}

export const catalogPaths = Object.freeze({
  configuration: path.join(siteRoot, 'src/data/channels.json'),
  resolved: path.join(siteRoot, '.generated/releases.json'),
});

const sha256Pattern = /^[0-9a-f]{64}$/;
const stablePattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const dependencyPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const catalogKeys = [
  'schemaVersion', 'mode', 'version', 'releaseUrl', 'manifestUrl', 'commit',
  'copilotSdkVersion', 'copilotRuntimeVersion', 'artifacts', 'channels',
];
const artifactKeys = [
  'platform', 'label', 'archive', 'format', 'binary', 'archiveUrl', 'checksumUrl',
  'archiveSha256', 'binarySha256',
];

function object(value, required, label, optional = []) {
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      required.some((key) => !Object.hasOwn(value, key)) ||
      Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key))) {
    throw new Error(`${label} has missing or unsupported fields`);
  }
  return value;
}

/** @internal Shared exact-version contract for native and package-manager metadata. */
export function stableVersion(value, label) {
  if (typeof value !== 'string' || value.length > 64 || value.trim() !== value ||
      !stablePattern.test(value) || value === '0.0.0') {
    throw new Error(`${label} must be an exact, non-placeholder stable X.Y.Z version`);
  }
  return value;
}

function sameKeys(value, expected) {
  return value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}

/** @internal Bounded callers reject ambiguous duplicate keys and excessive nesting. */
export function parseReleaseJSON(text, label) {
  let value;
  try {
    value = JSON.parse(text);
  } catch (error) {
    if (!(error instanceof SyntaxError)) throw error;
    throw new Error(`${label} must contain valid JSON`);
  }
  const stack = [];
  for (const [token] of text.matchAll(/"(?:[^"\\]|\\.)*"|[{}[\]:,]|[^\s{}[\]:,"]+/g)) {
    const current = stack.at(-1);
    if (token === '{' || token === '[') {
      if (stack.length >= 32) throw new Error(`${label} exceeds the JSON nesting limit`);
      stack.push({ keys: token === '{' ? new Set() : null, key: token === '{' });
    } else if (token === '}' || token === ']') {
      stack.pop();
    } else if (token === ',' && current?.keys) {
      current.key = true;
    } else if (token.startsWith('"') && current?.key) {
      const key = JSON.parse(token);
      if (current.keys.has(key)) throw new Error(`${label} has duplicate JSON object keys`);
      current.keys.add(key);
      current.key = false;
    }
  }
  return value;
}

/** @internal Upstream SDK/runtime pins use native SemVer, without a tag's v prefix. */
export function isDependencyVersion(value) {
  if (typeof value !== 'string' || value.length > 128 || value.trim() !== value ||
      !dependencyPattern.test(value)) return false;
  const prerelease = value.split('+', 1)[0].split('-').slice(1).join('-');
  return prerelease.split('.').every((part) => !/^\d+$/.test(part) || part === '0' || !part.startsWith('0'));
}

async function readText(filename, label, limit = 32 * 1024) {
  const file = await open(filename, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const info = await file.stat();
    if (!info.isFile() || info.size > limit) {
      throw new Error(`${label} must be a regular file of at most ${limit} bytes`);
    }
    const buffer = Buffer.alloc(limit + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await file.read(buffer, length, buffer.length - length, null);
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    if (length > limit) throw new Error(`${label} exceeds its ${limit}-byte limit`);
    return new TextDecoder('utf-8', { fatal: true }).decode(buffer.subarray(0, length));
  } finally {
    await file.close();
  }
}

function validateConfiguration(value) {
  object(value, ['schemaVersion', 'mode', 'repository', 'releaseTag', 'channels'], 'Channel configuration');
  if (value.schemaVersion !== 1 || !['pre-release', 'release'].includes(value.mode)) {
    throw new Error('Channel configuration requires schemaVersion 1 and an explicit pre-release or release mode');
  }
  if (value.repository !== releaseIdentities.repository) {
    throw new Error('Release repository must match the canonical VeVarunSharma/sodapop source identity');
  }
  if (value.releaseTag !== null) {
    if (typeof value.releaseTag !== 'string' || !value.releaseTag.startsWith('v')) {
      throw new Error('releaseTag must be null or an exact stable vX.Y.Z tag');
    }
    stableVersion(value.releaseTag.slice(1), 'releaseTag');
  }
  object(value.channels, ['npm', 'homebrew'], 'Channel configuration');
  object(value.channels.npm, ['verify', 'package'], 'npm configuration');
  object(value.channels.homebrew, ['verify', 'repository', 'formula'], 'Homebrew configuration');
  if (typeof value.channels.npm.verify !== 'boolean' ||
      value.channels.npm.package !== releaseIdentities.npmPackage) {
    throw new Error('npm configuration requires a boolean verify flag and the source package @sodapop-sh/cli');
  }
  const tap = value.channels.homebrew;
  if (typeof tap.verify !== 'boolean' || tap.formula !== releaseIdentities.homebrewFormula ||
      (tap.repository !== null && tap.repository !== releaseIdentities.homebrewRepository)) {
    throw new Error('Homebrew configuration must use the owned VeVarunSharma/homebrew-sodapop tap and sodapop formula');
  }
  if (tap.verify && tap.repository === null) {
    throw new Error('Confirm public tap ownership, then configure channels.homebrew.repository before enabling verify');
  }
  return value;
}

async function sourceIdentity(root) {
  const repositoryURL = `git+https://github.com/${releaseIdentities.repository}.git`;
  const unix = nativePlatforms.filter(({ platform }) => !platform.startsWith('windows/'));
  const names = nativePlatforms.map(({ platform }) => npmTarget(platform).name);
  const cli = parseReleaseJSON(await readText(path.join(root, 'npm/packages/cli/package.json'), 'Source npm package'), 'Source npm package');
  if (cli.name !== releaseIdentities.npmPackage || cli.repository?.url !== repositoryURL ||
      cli.homepage !== 'https://sodapop.sh' || typeof cli.version !== 'string' || cli.version.length === 0 ||
      !sameKeys(cli.bin, ['sodapop']) || cli.bin.sodapop !== 'bin/sodapop.js' ||
      typeof cli.engines?.node !== 'string' || cli.engines.node.trim() === '' || cli.engines.node.length > 64 ||
      /[\r\n]/.test(cli.engines.node)) {
    throw new Error('Source npm launcher identity, command, or Node requirement differs from the catalog contract');
  }
  // The builder replaces this development template's dependency set from the
  // release manifest. Validate any listed identities, not an assumed public set.
  if (Object.hasOwn(cli, 'optionalDependencies')) {
    object(cli.optionalDependencies, [], 'Source npm template optionalDependencies', names);
    if (Object.values(cli.optionalDependencies).some((version) => version !== cli.version)) {
      throw new Error('Source npm template optionalDependencies must use the exact template version');
    }
  }
  for (const { platform } of nativePlatforms) {
    const id = platform.replace('/', '-');
    const target = npmTarget(platform);
    const pkg = parseReleaseJSON(await readText(path.join(root, `npm/packages/${id}/package.json`), 'Source native npm package'), 'Source native npm package');
    if (pkg.name !== target.name || pkg.repository?.url !== repositoryURL ||
        pkg.version !== cli.version || JSON.stringify(pkg.os) !== JSON.stringify([target.os]) ||
        JSON.stringify(pkg.cpu) !== JSON.stringify([target.cpu]) ||
        (target.os === 'linux' && JSON.stringify(pkg.libc) !== '["glibc"]') ||
        !Array.isArray(pkg.files) || !pkg.files.includes(`bin/${target.binary}`)) {
      throw new Error(`Source npm package identity/platform differs for ${platform}`);
    }
  }
  const rawTemplate = (await readText(path.join(root, 'packaging/homebrew/Formula/sodapop.rb.tmpl'), 'Homebrew template')).replaceAll('\r\n', '\n');
  if (!['@RELEASE_BASE@', '@COPILOT_SDK_VERSION@', '@COPILOT_RUNTIME_VERSION@'].every((key) => rawTemplate.includes(key))) {
    throw new Error('Source Homebrew template is missing release/SDK/runtime placeholders');
  }
  const template = rawTemplate.replaceAll('@RELEASE_BASE@',
    `https://github.com/${releaseIdentities.repository}/releases/download/v@VERSION@`);
  if (!template.startsWith('class Sodapop < Formula\n') ||
      !template.includes('  homepage "https://sodapop.sh"\n') ||
      !template.includes('  version "@VERSION@"\n') ||
      !template.includes('    bin.install "sodapop"\n') ||
      (template.match(/^\s+url /gm) ?? []).length !== unix.length) {
    throw new Error('Source Homebrew formula identity differs from the catalog contract');
  }
  const placeholders = new Set(['@VERSION@', '@COPILOT_SDK_VERSION@', '@COPILOT_RUNTIME_VERSION@']);
  for (const { platform } of unix) {
    const id = platform.replace('/', '-');
    const url = `https://github.com/${releaseIdentities.repository}/releases/download/v@VERSION@/sodapop-@VERSION@-${id}.tar.gz`;
    const hash = `@${id.replaceAll('-', '_').toUpperCase()}_SHA256@`;
    placeholders.add(hash);
    if (template.split(`url "${url}"`).length !== 2 || template.split(hash).length !== 2) {
      throw new Error(`Source Homebrew archive identity differs for ${platform}`);
    }
  }
  if ([...template.matchAll(/@[A-Z0-9_]+@/g)].some(([key]) => !placeholders.has(key))) {
    throw new Error('Source Homebrew template has unknown placeholders');
  }
  return { homebrewTemplate: template, npmNodeRequirement: cli.engines.node };
}

/**
 * Shared server-only configuration/source checks used by both entry points.
 * @param {CatalogOptions} [options]
 * @returns {Promise<{configuration: ReleaseConfiguration, homebrewTemplate: string, npmNodeRequirement: string}>}
 */
export async function readReleaseInputs(options = {}) {
  const text = await readText(options.configPath ?? catalogPaths.configuration, 'Channel configuration');
  const configuration = validateConfiguration(parseReleaseJSON(text, 'Channel configuration'));
  const sources = await sourceIdentity(options.repositoryRoot ?? defaultRepositoryRoot);
  return { configuration, ...sources };
}

/** @returns {import('../src/lib/catalog-types.ts').UnpublishedChannel} */
function unpublished() {
  return { status: 'unpublished', command: null, version: null, url: null, platforms: [] };
}

/** @returns {import('../src/lib/catalog-types.ts').PreReleaseCatalog} */
function preReleaseCatalog() {
  return {
    schemaVersion: 1, mode: 'pre-release', version: null, releaseUrl: null, manifestUrl: null,
    commit: null, copilotSdkVersion: null, copilotRuntimeVersion: null, artifacts: [],
    channels: { npm: unpublished(), homebrew: unpublished() },
  };
}

/**
 * @internal Shared, explicit offline override; never a metadata-error fallback.
 * @param {NodeJS.ProcessEnv} [environment]
 * @returns {import('../src/lib/catalog-types.ts').PreReleaseCatalog | null}
 */
export function previewCatalog(environment = process.env) {
  if (environment.SODAPOP_SITE_PREVIEW === '') throw new Error('SODAPOP_SITE_PREVIEW must be 0 or 1');
  return isPreview(environment) ? { ...preReleaseCatalog(), preview: true } : null;
}

/**
 * Normalize the actual snake_case schema-1 native manifest. Asset existence and
 * sidecar bytes are checked by the resolver before this data can be persisted.
 * @param {unknown} value
 * @param {ReleaseConfiguration} configuration
 * @returns {PublishedReleaseCatalog}
 */
export function catalogFromManifest(value, configuration) {
  object(value, ['schema_version', 'version', 'commit', 'copilot_runtime_version', 'copilot_sdk_version', 'artifacts'], 'Release manifest');
  if (value.schema_version !== 1) throw new Error('Release manifest requires schema_version 1');
  const version = stableVersion(value.version, 'Manifest version');
  if (configuration.releaseTag !== null && configuration.releaseTag !== `v${version}`) {
    throw new Error('Manifest version does not match configured releaseTag');
  }
  if (typeof value.commit !== 'string' || value.commit.length !== 40 ||
      !/^[0-9a-f]{40}$/.test(value.commit) || /^0+$/.test(value.commit)) {
    throw new Error('Manifest commit must be a full lowercase Git commit');
  }
  if (!isDependencyVersion(value.copilot_runtime_version)) {
    throw new Error('Manifest copilot_runtime_version is invalid');
  }
  if (!isDependencyVersion(value.copilot_sdk_version)) {
    throw new Error('Manifest copilot_sdk_version is invalid');
  }
  if (!Array.isArray(value.artifacts) || value.artifacts.length !== nativePlatforms.length) {
    throw new Error('Release manifest must contain all five native platforms exactly once');
  }
  const entries = new Map();
  for (const artifact of value.artifacts) {
    object(artifact, ['platform', 'archive', 'archive_sha256', 'binary_sha256'], 'Manifest artifact');
    if (!nativePlatforms.some(({ platform }) => platform === artifact.platform) || entries.has(artifact.platform)) {
      throw new Error('Release manifest contains an unsupported or duplicate platform');
    }
    if (typeof artifact.archive_sha256 !== 'string' || artifact.archive_sha256.length !== 64 ||
        !sha256Pattern.test(artifact.archive_sha256) || typeof artifact.binary_sha256 !== 'string' ||
        artifact.binary_sha256.length !== 64 || !sha256Pattern.test(artifact.binary_sha256)) {
      throw new Error('Manifest artifact requires lowercase archive_sha256 and binary_sha256 digests');
    }
    entries.set(artifact.platform, artifact);
  }
  const base = `https://github.com/${configuration.repository}/releases/download/v${version}`;
  /** @type {import('../src/lib/catalog-types.ts').NativeArtifact[]} */
  const artifacts = nativePlatforms.map(({ platform, label }) => {
    const artifact = entries.get(platform);
    const stem = `sodapop-${version}-${platform.replace('/', '-')}`;
    const windows = platform === 'windows/amd64';
    if (artifact.archive !== `${stem}${windows ? '.zip' : '.tar.gz'}`) {
      throw new Error(`Manifest archive filename does not match version/platform for ${platform}`);
    }
    return {
      platform, label, archive: artifact.archive,
      format: artifact.archive.endsWith('.zip') ? 'zip' : 'tar.gz',
      binary: windows ? 'sodapop.exe' : 'sodapop',
      archiveUrl: `${base}/${artifact.archive}`,
      checksumUrl: `${base}/${artifact.archive}.sha256`,
      archiveSha256: artifact.archive_sha256, binarySha256: artifact.binary_sha256,
    };
  });
  return {
    schemaVersion: 1, mode: 'release', version,
    releaseUrl: `https://github.com/${configuration.repository}/releases/tag/v${version}`,
    manifestUrl: `${base}/sodapop-${version}-manifest.json`,
    commit: value.commit, copilotSdkVersion: value.copilot_sdk_version,
    copilotRuntimeVersion: value.copilot_runtime_version, artifacts,
    channels: { npm: unpublished(), homebrew: unpublished() },
  };
}

/**
 * Fail closed on local resolved metadata, including URLs and command strings.
 * @param {unknown} value
 * @param {ReleaseConfiguration} configuration
 * @returns {ReleaseCatalog}
 */
export function validateCatalog(value, configuration) {
  object(value, catalogKeys, 'Resolved catalog');
  if (value.schemaVersion !== 1 || value.mode !== configuration.mode) {
    throw new Error('Resolved catalog schema/mode does not match channel configuration; run release:sync');
  }
  /** @type {ReleaseCatalog} */
  let expected = preReleaseCatalog();
  if (value.mode === 'release') {
    if (!Array.isArray(value.artifacts)) throw new Error('Resolved catalog artifacts must be an array');
    expected = catalogFromManifest({
      schema_version: 1, version: value.version, commit: value.commit,
      copilot_runtime_version: value.copilotRuntimeVersion,
      copilot_sdk_version: value.copilotSdkVersion,
      artifacts: value.artifacts.map((artifact) => {
        object(artifact, artifactKeys, 'Resolved artifact');
        return {
          platform: artifact.platform, archive: artifact.archive,
          archive_sha256: artifact.archiveSha256, binary_sha256: artifact.binarySha256,
        };
      }),
    }, configuration);
  }
  for (const key of catalogKeys.filter((key) => !['artifacts', 'channels'].includes(key))) {
    if (value[key] !== expected[key]) throw new Error(`Resolved catalog has an invalid ${key}`);
  }
  if (!Array.isArray(value.artifacts) || value.artifacts.length !== expected.artifacts.length) {
    throw new Error('Resolved catalog has an invalid native platform set');
  }
  for (const artifact of expected.artifacts) {
    const actual = value.artifacts.find((entry) => entry.platform === artifact.platform);
    if (artifactKeys.some((key) => actual[key] !== artifact[key])) {
      throw new Error(`Resolved catalog has invalid archive metadata/URLs for ${artifact.platform}`);
    }
  }
  object(value.channels, ['npm', 'homebrew'], 'Resolved channels');
  for (const name of ['npm', 'homebrew']) {
    const channel = object(value.channels[name], ['status', 'command', 'version', 'url', 'platforms'], `Resolved ${name} channel`);
    if (!Array.isArray(channel.platforms)) throw new Error(`Resolved ${name} channel platforms must be an array`);
    if (channel.status === 'unpublished') {
      if (channel.command !== null || channel.version !== null || channel.url !== null || channel.platforms.length !== 0) {
        throw new Error(`Unpublished ${name} channel requires null command/version/URL and an empty platform list`);
      }
    } else if (channel.status === 'published') {
      const url = name === 'npm' ? 'https://www.npmjs.com/package/@sodapop-sh/cli'
        : `https://github.com/${releaseIdentities.homebrewRepository}`;
      if (expected.mode !== 'release' || !configuration.channels[name].verify ||
          channel.command !== installCommands[name] || channel.version !== expected.version || channel.url !== url) {
        throw new Error(`Published ${name} channel identity, version, command, or configuration is invalid`);
      }
      const declared = new Set(channel.platforms);
      if (declared.size === 0 || declared.size !== channel.platforms.length ||
          channel.platforms.some((platform) => !expected.artifacts.some((artifact) => artifact.platform === platform))) {
        throw new Error(`Published ${name} channel has invalid declared platforms`);
      }
      const platforms = expected.artifacts.filter(({ platform }) => declared.has(platform)).map(({ platform }) => platform);
      if (name === 'homebrew' && (platforms.length !== 4 || platforms.includes('windows/amd64'))) {
        throw new Error('Published Homebrew channel requires exactly the four Unix platforms');
      }
      expected.channels[name] = { status: 'published', command: installCommands[name], version: expected.version, url, platforms };
    } else {
      throw new Error(`Resolved ${name} channel has an invalid publication status`);
    }
  }
  return expected;
}

/**
 * The only catalog API needed by Astro pages and prepare.mjs. Never performs
 * network I/O or writes files. Explicit pre-release mode ignores old snapshots.
 * Release mode requires a validated, locally refreshed public snapshot.
 * Channel snapshots require explicit platforms; run release:sync for older
 * snapshots rather than inferring npm support from the native download list.
 * Explicit preview mode bypasses snapshots and returns preview:true, without
 * commands or download links, even when channels.json selects release mode.
 * @param {CatalogOptions} [options]
 * @returns {Promise<ReleaseCatalog>}
 */
export async function readCatalog(options = {}) {
  const preview = previewCatalog(options.environment ?? process.env);
  if (preview) return preview;
  const { configuration } = await readReleaseInputs(options);
  if (configuration.mode === 'pre-release') return preReleaseCatalog();
  const filename = options.resolvedPath ?? catalogPaths.resolved;
  let text;
  try {
    text = await readText(filename, 'Resolved release metadata', 128 * 1024);
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
    throw new Error('Release mode requires local .generated/releases.json; confirm public repository access and run npm --prefix site run release:sync');
  }
  const record = object(parseReleaseJSON(text, 'Resolved release metadata'),
    ['schemaVersion', 'source', 'repository', 'checkedAt', 'catalog'], 'Resolved release envelope');
  if (record.schemaVersion !== 1 || record.repository !== configuration.repository ||
      !['public', 'fixture'].includes(record.source) ||
      typeof record.checkedAt !== 'string' || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(record.checkedAt) ||
      !Number.isFinite(Date.parse(record.checkedAt)) ||
      new Date(record.checkedAt).toISOString() !== record.checkedAt) {
    throw new Error('Resolved release provenance is invalid; run release:sync');
  }
  if (record.source === 'fixture' &&
      !(options.allowFixtures === true && options.resolvedPath &&
        path.resolve(options.resolvedPath) !== path.resolve(catalogPaths.resolved))) {
    throw new Error('Production builds cannot use fixture release catalogs; fixtures require explicitly injected test paths and allowFixtures');
  }
  return validateCatalog(record.catalog, configuration);
}
