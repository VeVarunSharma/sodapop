import { lstat, mkdir, mkdtemp, readFile, realpath, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';

const ownedDirectories = new Set([
  'src/content/docs/docs',
  'public/assets/brand',
  'public/assets/demos',
]);

export function relativePath(value, label = 'Path') {
  if (typeof value !== 'string' || !value || path.isAbsolute(value) ||
      /[\\\u0000-\u001f\u007f?#%]/u.test(value) ||
      value.split('/').some((part) => !part || part === '.' || part === '..')) {
    throw new Error(`${label} must be a normalized repository-relative path: ${String(value)}`);
  }
  return value;
}

export function publicSourcePath(value) {
  relativePath(value, 'Public source');
  const parts = value.split('/');
  const excluded = parts.some((part) => part.startsWith('.') ||
    /^(?:AGENTS\.md|node_modules|vendor|sessions?|session-state|private|notes)$/i.test(part) ||
    /^(?:agent-guidance|(?:internal|private|session)[-_](?:notes?|plans?))(?:\.|$)/i.test(part) ||
    /(?:^|\.)env(?:\.|$)/i.test(part));
  if (excluded || (parts[0] === 'docs' && parts.includes('internal'))) {
    throw new Error(`Excluded source cannot be published or linked: ${value}`);
  }
  return value;
}

async function optionalStat(filename) {
  try {
    return await lstat(filename);
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw error;
  }
}

function within(root, filename) {
  const relative = path.relative(root, filename);
  return relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

export async function repositoryFile(root, source) {
  publicSourcePath(source);
  const canonicalRoot = await realpath(root);
  let filename = canonicalRoot;
  const parts = source.split('/');
  for (let index = 0; index < parts.length; index++) {
    filename = path.join(filename, parts[index]);
    const info = await optionalStat(filename);
    if (!info) throw new Error(`Missing source or link target: ${source}`);
    if (info.isSymbolicLink()) {
      const target = await realpath(filename);
      if (!within(canonicalRoot, target)) {
        throw new Error(`Source or link target resolves outside the repository: ${source}`);
      }
      throw new Error(`Symlink sources and link targets are not allowed: ${source}`);
    }
    if (index < parts.length - 1 ? !info.isDirectory() : !info.isFile()) {
      throw new Error(`Source or link target is not a regular file: ${source}`);
    }
  }
  return filename;
}

export async function readRepositoryFile(root, source) {
  return readFile(await repositoryFile(root, source));
}

async function ownedParent(root, relative) {
  const canonicalRoot = await realpath(root);
  let directory = canonicalRoot;
  for (const part of relative.split('/')) {
    directory = path.join(directory, part);
    let info = await optionalStat(directory);
    if (!info) {
      await mkdir(directory);
      info = await lstat(directory);
    }
    if (info.isSymbolicLink() || !info.isDirectory()) {
      throw new Error(`Generated output must use real directories inside the site: ${directory}`);
    }
  }
  return directory;
}

export async function replaceOwnedDirectory(root, relative, files) {
  if (!ownedDirectories.has(relative)) {
    throw new Error(`Refusing to replace an unowned generated directory: ${relative}`);
  }
  for (const filename of files.keys()) relativePath(filename, 'Generated filename');
  const parent = await ownedParent(root, path.posix.dirname(relative));
  const destination = path.join(parent, path.posix.basename(relative));
  const existing = await optionalStat(destination);
  if (existing && (existing.isSymbolicLink() || !existing.isDirectory())) {
    throw new Error(`Generated output must be a real directory: ${destination}`);
  }

  const temporary = await mkdtemp(path.join(parent, `.${path.basename(destination)}-content-`));
  const next = path.join(temporary, 'next');
  const previous = path.join(temporary, 'previous');
  let preservePrevious = false;
  try {
    await mkdir(next);
    for (const [filename, contents] of [...files].sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) {
      const target = path.join(next, filename);
      await mkdir(path.dirname(target), { recursive: true });
      await writeFile(target, contents);
    }
    if (existing) await rename(destination, previous);
    try {
      await rename(next, destination);
    } catch (error) {
      if (existing) {
        try {
          await rename(previous, destination);
        } catch (restoreError) {
          preservePrevious = true;
          throw new AggregateError([error, restoreError],
            `Could not restore generated output; the prior copy remains at ${previous}`);
        }
      }
      throw error;
    }
  } finally {
    if (!preservePrevious) await rm(temporary, { recursive: true, force: true });
  }
}
