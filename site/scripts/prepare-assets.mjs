import path from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';
import {
  normalizeBase,
  repositoryRoot as defaultRepositoryRoot,
  siteConfiguration,
  siteRoot as defaultSiteRoot,
} from './site-config.mjs';
import { readRepositoryFile, replaceOwnedDirectory } from './content-files.mjs';

const brandFiles = [
  'sodapop-mascot.webp',
  'sodapop-wordmark.svg',
  'sodapop-lockup-on-light.svg',
  'sodapop-lockup-on-dark.svg',
  'sodapop-favicon.svg',
  'sodapop-favicon.png',
  'favicon-16.png',
  'favicon-32.png',
  'apple-touch-icon.png',
  'favicon.ico',
  'sodapop-og.png',
  'sodapop-oauth.png',
];
const demoNames = ['overview', 'commands', 'themes', 'diff'];

export const publicAssets = Object.freeze([
  ...brandFiles.map((filename) => ({
    source: `images/${filename}`,
    destination: `assets/brand/${filename}`,
  })),
  ...demoNames.flatMap((name) =>
    ['gif', 'png'].map((extension) => ({
      source: `docs/assets/demos/${name}.${extension}`,
      destination: `assets/demos/${name}.${extension}`,
    }))),
].map((asset) => Object.freeze(asset)));

function iconDimensions(bytes, source) {
  if (bytes.length < 6 || bytes.readUInt16LE(0) !== 0 || bytes.readUInt16LE(2) !== 1) {
    throw new Error(`Invalid ICO header: ${source}`);
  }
  const count = bytes.readUInt16LE(4);
  if (!count || count > 256 || bytes.length < 6 + count * 16) {
    throw new Error(`Invalid ICO image directory: ${source}`);
  }
  let largest;
  for (let index = 0; index < count; index++) {
    const start = 6 + index * 16;
    const size = bytes.readUInt32LE(start + 8);
    const offset = bytes.readUInt32LE(start + 12);
    if (!size || offset < 6 + count * 16 || offset + size > bytes.length) {
      throw new Error(`Truncated ICO image: ${source}`);
    }
    const width = bytes[start] || 256;
    const height = bytes[start + 1] || 256;
    if (!largest || width * height > largest.width * largest.height) largest = { width, height };
  }
  return { ...largest, format: 'ico' };
}

async function imageDimensions(bytes, source) {
  const extension = path.extname(source).slice(1);
  if (extension === 'ico') return iconDimensions(bytes, source);
  const metadata = await sharp(bytes, { failOn: 'error' }).metadata();
  const height = metadata.pageHeight ?? metadata.height;
  if (metadata.format !== extension || !Number.isSafeInteger(metadata.width) ||
      !Number.isSafeInteger(height) || metadata.width <= 0 || height <= 0) {
    throw new Error(`Invalid image dimensions or unexpected format: ${source}`);
  }
  return { width: metadata.width, height, format: metadata.format };
}

/**
 * Stage the approved original artwork and recordings plus lossless WebP demo posters.
 * @param {{repositoryRoot?: string, siteRoot?: string, base?: string}} options
 * @returns {Promise<{
 *   base: string,
 *   assets: Array<{source: string, destination: string, url: string, width: number, height: number, bytes: number, format: string}>,
 *   posters: Array<{source: string, destination: string, url: string, width: number, height: number, bytes: number, format: string}>
 * }>}
 */
export async function prepareAssets(options = {}) {
  const repositoryRoot = options.repositoryRoot ?? defaultRepositoryRoot;
  const siteRoot = options.siteRoot ?? defaultSiteRoot;
  const base = normalizeBase(options.base ?? siteConfiguration().base);
  const groups = { brand: new Map(), demos: new Map() };
  const assets = [];
  const posters = [];

  for (const asset of publicAssets) {
    const bytes = await readRepositoryFile(repositoryRoot, asset.source);
    const dimensions = await imageDimensions(bytes, asset.source);
    const [, group, filename] = asset.destination.split('/');
    groups[group].set(filename, bytes);
    assets.push({ ...asset, url: `${base}${asset.destination}`, ...dimensions, bytes: bytes.length });
    if (group === 'demos' && dimensions.format === 'png') {
      const posterFilename = `${path.basename(filename, '.png')}.webp`;
      const posterBytes = await sharp(bytes).webp({ lossless: true, effort: 6 }).toBuffer();
      const destination = `assets/demos/${posterFilename}`;
      groups.demos.set(posterFilename, posterBytes);
      posters.push({
        source: asset.source,
        destination,
        url: `${base}${destination}`,
        width: dimensions.width,
        height: dimensions.height,
        bytes: posterBytes.length,
        format: 'webp',
      });
    }
  }
  for (const [group, files] of Object.entries(groups)) {
    await replaceOwnedDirectory(siteRoot, `public/assets/${group}`, files);
  }
  return { base, assets, posters };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const result = await prepareAssets();
  console.info(`Prepared ${result.assets.length} approved public assets.`);
}
