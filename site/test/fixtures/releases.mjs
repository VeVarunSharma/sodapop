/** Synthetic public-shaped metadata, reachable only through injected test fetch. */
import { createHash } from 'node:crypto';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { repositoryRoot } from '../../scripts/site-config.mjs';

export const repository = 'VeVarunSharma/sodapop';
export const tap = 'VeVarunSharma/homebrew-sodapop';
export const api = `https://api.github.com/repos/${repository}`;
export const tapAPI = `https://api.github.com/repos/${tap}`;
export const registry = 'https://registry.npmjs.org';
export const version = '1.2.3';
export const platforms = ['darwin/arm64', 'darwin/amd64', 'linux/arm64', 'linux/amd64', 'windows/amd64'];
const sha256 = (text) => createHash('sha256').update(text).digest('hex');
const encodeJSON = (value) => Buffer.from(`${JSON.stringify(value)}\n`);

export async function releaseFixture(t, {
  mode = 'release', npm = false, homebrew = false, windowsZip = true, sdk = true,
  npmPlatforms = platforms.slice(0, 4),
} = {}) {
  const root = await mkdtemp(path.join(tmpdir(), 'sodapop-site-releases-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const routes = new Map();
  const f = {
    root, routes, requests: [],
    configPath: path.join(root, 'channels.json'),
    resolvedPath: path.join(root, '.generated/releases.json'),
    configuration: {
      schemaVersion: 1, mode, repository, releaseTag: null,
      channels: {
        npm: { verify: npm, package: '@sodapop/cli' },
        homebrew: { verify: homebrew, repository: homebrew ? tap : null, formula: 'sodapop' },
      },
    },
    repository: {
      full_name: repository, private: false, visibility: 'public',
      html_url: `https://github.com/${repository}`, url: api, default_branch: 'main',
    },
    tap: {
      full_name: tap, private: false, visibility: 'public',
      html_url: `https://github.com/${tap}`, url: tapAPI, default_branch: 'main',
    },
    manifestName: `sodapop-${version}-manifest.json`,
    manifest: {
      schema_version: 1, version, commit: createHash('sha1').update('fixture commit').digest('hex'),
      copilot_runtime_version: '1.0.83', ...(sdk ? { copilot_sdk_version: '1.0.13' } : {}),
      artifacts: platforms.map((platform) => ({
        platform,
        archive: `sodapop-${version}-${platform.replace('/', '-')}${windowsZip && platform === 'windows/amd64' ? '.zip' : '.tar.gz'}`,
        archive_sha256: sha256(`fixture archive ${platform}`),
        binary_sha256: sha256(`fixture binary ${platform}`),
      })),
    },
    release: {
      id: 123, tag_name: `v${version}`, draft: false, prerelease: false,
      published_at: '2026-09-01T12:00:00Z',
      html_url: `https://github.com/${repository}/releases/tag/v${version}`,
      url: `${api}/releases/123`, assets_url: `${api}/releases/123/assets`, assets: [],
    },
    npm: new Map(),
    saveConfiguration() {
      return writeFile(this.configPath, `${JSON.stringify(this.configuration)}\n`);
    },
  };
  const base = `https://github.com/${repository}/releases/download/v${version}`;
  let assetID = 1;
  function addAsset(name, size, hash) {
    const id = assetID++;
    const asset = {
      id, name, size, state: 'uploaded', digest: `sha256:${hash}`,
      browser_download_url: `${base}/${name}`, url: `${api}/releases/assets/${id}`,
    };
    f.release.assets.push(asset);
    return asset;
  }
  f.setJSON = (url, value, status = 200) => {
    routes.set(url, () => new Response(encodeJSON(value), { status, headers: { 'content-type': 'application/json' } }));
  };
  f.setBytes = (url, value, status = 200, headers = {}) => {
    routes.set(url, () => new Response(value, { status, headers }));
  };
  f.setManifestText = (text) => {
    const bytes = Buffer.from(text);
    const asset = f.release.assets.find(({ name }) => name === f.manifestName);
    asset.size = bytes.length;
    asset.digest = `sha256:${sha256(bytes)}`;
    f.setBytes(`${base}/${f.manifestName}`, bytes);
  };
  f.refreshManifest = () => f.setManifestText(encodeJSON(f.manifest));
  f.setChecksum = (platform, text) => {
    const artifact = f.manifest.artifacts.find((entry) => entry.platform === platform);
    const name = `${artifact.archive}.sha256`;
    const bytes = Buffer.from(text);
    const asset = f.release.assets.find((entry) => entry.name === name);
    asset.size = bytes.length;
    asset.digest = `sha256:${sha256(bytes)}`;
    f.setBytes(`${base}/${name}`, bytes);
  };
  for (const artifact of f.manifest.artifacts) {
    addAsset(artifact.archive, 300 * 1024 * 1024, artifact.archive_sha256);
    addAsset(`${artifact.archive}.sha256`, 1, sha256('placeholder'));
    f.setChecksum(artifact.platform, `${artifact.archive_sha256}  ${artifact.archive}\n`);
  }
  addAsset(f.manifestName, 1, sha256('placeholder'));
  f.refreshManifest();
  f.gitRef = {
    ref: `refs/tags/v${version}`,
    object: { type: 'commit', sha: f.manifest.commit, url: `${api}/git/commits/${f.manifest.commit}` },
  };
  f.setJSON(api, f.repository);
  f.setJSON(`${api}/releases/latest`, f.release);
  f.setJSON(`${api}/releases/tags/v${version}`, f.release);
  f.setJSON(`${api}/git/ref/tags/v${version}`, f.gitRef);
  f.setJSON(tapAPI, f.tap);
  for (const id of ['cli', ...platforms.map((platform) => platform.replace('/', '-'))]) {
    const metadata = JSON.parse(await readFile(path.join(repositoryRoot, `npm/packages/${id}/package.json`), 'utf8'));
    delete metadata.private;
    metadata.version = version;
    const { artifacts, ...header } = f.manifest;
    if (id === 'cli') {
      metadata.optionalDependencies = Object.fromEntries(npmPlatforms.map((platform) => [
        `@sodapop/${platform.replace('/', '-')}`, version,
      ]));
      metadata.sodapop = {
        ...header, artifacts: structuredClone(artifacts.filter(({ platform }) => npmPlatforms.includes(platform))),
      };
    } else {
      const artifact = f.manifest.artifacts.find(({ platform }) => platform.replace('/', '-') === id);
      metadata.sodapop = { ...header, artifact: structuredClone(artifact) };
    }
    metadata.dist = {
      tarball: `${registry}/@sodapop/${id}/-/${id}-${version}.tgz`,
      integrity: `sha512-${createHash('sha512').update(`fixture npm ${id}`).digest('base64')}`,
    };
    f.npm.set(id, metadata);
    f.setJSON(`${registry}/@sodapop%2f${id}/${id === 'cli' ? 'latest' : version}`, metadata);
  }
  f.template = await readFile(path.join(repositoryRoot, 'packaging/homebrew/Formula/sodapop.rb.tmpl'), 'utf8');
  f.formula = f.template.replaceAll('@RELEASE_BASE@', base).replaceAll('@VERSION@', version)
    .replaceAll('@COPILOT_SDK_VERSION@', f.manifest.copilot_sdk_version ?? 'missing')
    .replaceAll('@COPILOT_RUNTIME_VERSION@', f.manifest.copilot_runtime_version);
  for (const artifact of f.manifest.artifacts.slice(0, 4)) {
    f.formula = f.formula.replaceAll(`@${artifact.platform.replace('/', '_').toUpperCase()}_SHA256@`, artifact.archive_sha256);
  }
  f.setFormula = (formula) => {
    f.formula = formula;
    const content = Buffer.from(formula);
    f.formulaFile = {
      type: 'file', path: 'Formula/sodapop.rb', encoding: 'base64', size: content.length,
      content: content.toString('base64'),
      sha: createHash('sha1').update(`blob ${content.length}\0`).update(content).digest('hex'),
      html_url: `https://github.com/${tap}/blob/main/Formula/sodapop.rb`,
      download_url: `https://raw.githubusercontent.com/${tap}/main/Formula/sodapop.rb`,
    };
    f.setJSON(`${tapAPI}/contents/Formula/sodapop.rb`, f.formulaFile);
  };
  f.setFormula(f.formula);
  f.fetch = async (url, options) => {
    f.requests.push({ url, options });
    const route = routes.get(url);
    if (!route) throw new Error(`Unexpected fixture request: ${url}`);
    return route(url, options);
  };
  f.options = {
    configPath: f.configPath, resolvedPath: f.resolvedPath, fetch: f.fetch,
    environment: { SODAPOP_SITE_PREVIEW: '0' },
  };
  f.readOptions = {
    configPath: f.configPath, resolvedPath: f.resolvedPath, allowFixtures: true,
    environment: { SODAPOP_SITE_PREVIEW: '0' },
  };
  f.copySources = async () => {
    const source = path.join(root, 'source');
    const names = ['cli', ...platforms.map((platform) => platform.replace('/', '-'))];
    for (const relative of [
      ...names.map((id) => `npm/packages/${id}/package.json`),
      'packaging/homebrew/Formula/sodapop.rb.tmpl',
    ]) {
      const destination = path.join(source, relative);
      await mkdir(path.dirname(destination), { recursive: true });
      await copyFile(path.join(repositoryRoot, relative), destination);
    }
    f.options.repositoryRoot = source;
    f.readOptions.repositoryRoot = source;
    return source;
  };
  await f.saveConfiguration();
  return f;
}
