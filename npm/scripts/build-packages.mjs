#!/usr/bin/env node

import {
  chmodSync, constants, copyFileSync, lstatSync, mkdirSync, mkdtempSync, readFileSync,
  readdirSync, realpathSync, renameSync, rmSync, writeFileSync
} from "node:fs";
import { homedir } from "node:os";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

export const npmRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const repositoryRoot = path.resolve(npmRoot, "..");
const require = createRequire(import.meta.url);
const { hashFile, TARGETS } = require("../packages/cli/lib/launcher.js");
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?$/;
const digest = /^[a-f0-9]{64}$/;

export function writeJSON(filename, value) {
  writeFileSync(filename, `${JSON.stringify(value, null, 2)}\n`);
}

export function parseArguments(args) {
  const options = { releaseDirectory: path.join(repositoryRoot, "dist"), output: path.join(npmRoot, "dist") };
  const names = { "--version": "version", "--release-dir": "releaseDirectory", "--output": "output", "--manifest": "manifest", "--platforms": "platforms" };
  const seen = new Set();
  for (let i = 0; i < args.length; i += 2) {
    const key = names[args[i]];
    if (!key || seen.has(key) || !args[i + 1] || args[i + 1].startsWith("--")) {
      throw new Error(`invalid or repeated argument ${args[i]}; use --version VERSION --manifest FILE [--release-dir DIR] [--output NEW_DIR] [--platforms os/arch,...]`);
    }
    seen.add(key);
    options[key] = args[i + 1];
  }
  if (!options.manifest) throw new Error("--manifest is required");
  for (const key of ["manifest", "releaseDirectory", "output"]) options[key] = path.resolve(options[key]);
  return options;
}

export function validateVersion(version) {
  if (typeof version !== "string" || version.length > 64 || !semver.test(version)) {
    throw new Error("--version must be a valid semantic version without build metadata, at most 64 characters");
  }
}

export function readManifest(options, bytes = readFileSync(options.manifest)) {
  validateVersion(options.version);
  const manifest = JSON.parse(bytes);
  const pins = readFileSync(path.join(repositoryRoot, "internal/runtimebundle/version.go"), "utf8");
  const sdk = pins.match(/\bSDKVersion\s*=\s*"([^"]+)"/)?.[1];
  const runtime = pins.match(/\bVersion\s*=\s*"([^"]+)"/)?.[1];
  const moduleSDK = readFileSync(path.join(repositoryRoot, "go.mod"), "utf8").match(/github\.com\/github\/copilot-sdk\/go v(\S+)/)?.[1];
  if (manifest.schema_version !== 1 || manifest.version !== options.version ||
      !/^[a-f0-9]{40}$/.test(manifest.commit) || !sdk || !runtime || sdk !== moduleSDK ||
      manifest.copilot_sdk_version !== sdk || manifest.copilot_runtime_version !== runtime) {
    throw new Error("manifest schema, version, commit, or repository SDK/runtime pins mismatch");
  }
  if (!Array.isArray(manifest.artifacts) || manifest.artifacts.length === 0) {
    throw new Error("manifest must declare available platforms");
  }
  const platforms = new Set();
  const binaries = new Set();
  for (const artifact of manifest.artifacts) {
    const target = Object.values(TARGETS).find((target) => target.platform === artifact?.platform);
    if (!target || platforms.has(artifact.platform)) throw new Error("manifest has unsupported or ambiguous platform entries");
    const name = `sodapop-${options.version}-${artifact.platform.replace("/", "-")}`;
    const extension = target.os === "win32" ? ".zip" : ".tar.gz";
    if (artifact.archive !== `${name}${extension}` || !digest.test(artifact.archive_sha256) || !digest.test(artifact.binary_sha256)) {
      throw new Error(`manifest has invalid archive or hashes for ${artifact.platform}`);
    }
    if (binaries.has(artifact.binary_sha256)) throw new Error("manifest reuses binary bytes across different platforms");
    platforms.add(artifact.platform);
    binaries.add(artifact.binary_sha256);
  }
  if (options.platforms !== undefined) {
    const declared = options.platforms.split(",");
    if (new Set(declared).size !== declared.length || declared.length !== platforms.size || declared.some((p) => !platforms.has(p))) {
      throw new Error("--platforms must exactly match the manifest's declared available platforms");
    }
  }
  return manifest;
}

export function releaseControl(args, options = {}) {
  const helper = options.helper ?? process.env.SODAPOP_RELEASECTL;
  const command = helper ? path.resolve(helper) : "go";
  const argv = helper ? args : ["run", "./scripts/releasectl", ...args];
  const result = spawnSync(command, argv, {
    cwd: repositoryRoot, encoding: "utf8", maxBuffer: 8 * 1024 * 1024,
    env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" }
  });
  if (result.error || result.status !== 0) {
    throw new Error(`releasectl ${args[0]} failed: ${result.error?.message || result.stderr.trim() || `exit ${result.status}`}`);
  }
  return result.stdout;
}

function isWithin(child, parent) {
  const relative = path.relative(parent, child);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
}

export function validateOutput(output, releaseDirectory) {
  const resolved = path.resolve(output);
  const releases = realpathSync(releaseDirectory);
  if ([repositoryRoot, npmRoot, homedir(), path.parse(resolved).root].some((p) => isWithin(p, resolved)) ||
      isWithin(resolved, releases)) {
    throw new Error("--output must be a new scoped build directory, outside the release input");
  }
  const parts = [];
  let current = resolved;
  while (current !== path.dirname(current)) {
    parts.unshift(current);
    current = path.dirname(current);
  }
  for (const item of parts) {
    let stat;
    try {
      stat = lstatSync(item);
    } catch (error) {
      if (error.code === "ENOENT") continue;
      throw error;
    }
    if (stat.isSymbolicLink()) throw new Error("--output and its parents must not be symlinks");
    if (item === resolved) throw new Error("--output already exists; select a new directory (existing output is never deleted)");
    if (!stat.isDirectory()) throw new Error("--output parent is not a directory");
  }
  // Requiring an existing parent avoids creating arbitrary directory trees on bad input.
  realpathSync(path.dirname(resolved));
  return resolved;
}

function copyRegular(source, destination) {
  if (!lstatSync(source).isFile()) throw new Error(`expected regular payload file: ${source}`);
  mkdirSync(path.dirname(destination), { recursive: true });
  copyFileSync(source, destination, constants.COPYFILE_FICLONE);
}

function copyLicenses(source, destination) {
  if (!lstatSync(source).isDirectory()) throw new Error("LICENSES must be a regular directory");
  let count = 0;
  for (const entry of readdirSync(source, { withFileTypes: true })) {
    const from = path.join(source, entry.name);
    const to = path.join(destination, entry.name);
    if (entry.isDirectory()) count += copyLicenses(from, to);
    else {
      copyRegular(from, to);
      count += 1;
    }
  }
  return count;
}

export function buildPackages(options) {
  const output = validateOutput(options.output, options.releaseDirectory);
  const manifestBytes = readFileSync(options.manifest);
  const manifest = readManifest(options, manifestBytes);
  const staging = mkdtempSync(path.join(path.dirname(output), ".sodapop-npm-"));
  try {
    const snapshot = path.join(staging, "manifest.json");
    writeFileSync(snapshot, manifestBytes);
    for (const artifact of manifest.artifacts) {
      releaseControl(["verify", "--dir", options.releaseDirectory, "--manifest", snapshot,
        "--platform", artifact.platform], options);
    }
    const cliSource = path.join(npmRoot, "packages", "cli");
    const cliOutput = path.join(staging, "cli");
    for (const entry of ["bin/sodapop.js", "lib/launcher.js", "README.md"]) {
      copyRegular(path.join(cliSource, entry), path.join(cliOutput, entry));
    }
    for (const entry of ["LICENSE", "THIRD_PARTY_NOTICES.md"]) {
      copyRegular(path.join(repositoryRoot, entry), path.join(cliOutput, entry));
    }
    chmodSync(path.join(cliOutput, "bin/sodapop.js"), 0o755);
    const cli = JSON.parse(readFileSync(path.join(cliSource, "package.json"), "utf8"));
    delete cli.private;
    cli.version = manifest.version;
    cli.optionalDependencies = {};
    cli.sodapop = manifest;

    for (const artifact of manifest.artifacts) {
      const target = Object.values(TARGETS).find((target) => target.platform === artifact.platform);
      const id = artifact.platform.replace("/", "-");
      const extracted = path.join(staging, `.extract-${id}`);
      releaseControl(["extract", "--dir", options.releaseDirectory, "--manifest", snapshot,
        "--platform", artifact.platform, "--output", extracted], options);
      const source = path.join(extracted, `sodapop-${manifest.version}-${id}`);
      const destination = path.join(staging, "platforms", id);
      copyRegular(path.join(source, target.binary), path.join(destination, "bin", target.binary));
      const binary = path.join(destination, "bin", target.binary);
      if (hashFile(binary) !== artifact.binary_sha256) throw new Error(`extracted binary hash mismatch: ${artifact.platform}`);
      chmodSync(binary, 0o755);
      for (const entry of ["LICENSE", "THIRD_PARTY_NOTICES.md"]) {
        copyRegular(path.join(source, entry), path.join(destination, entry));
      }
      if (copyLicenses(path.join(source, "LICENSES"), path.join(destination, "LICENSES")) === 0) {
        throw new Error("release has no upstream license material");
      }
      copyRegular(path.join(npmRoot, "packages/platform-README.md"), path.join(destination, "README.md"));
      const metadata = JSON.parse(readFileSync(path.join(npmRoot, "packages", id, "package.json"), "utf8"));
      delete metadata.private;
      metadata.version = manifest.version;
      metadata.sodapop = {
        schema_version: manifest.schema_version, version: manifest.version, commit: manifest.commit,
        copilot_sdk_version: manifest.copilot_sdk_version, copilot_runtime_version: manifest.copilot_runtime_version,
        artifact
      };
      writeJSON(path.join(destination, "package.json"), metadata);
      cli.optionalDependencies[target.package] = manifest.version;
      rmSync(extracted, { recursive: true });
    }
    writeJSON(path.join(cliOutput, "package.json"), cli);
    rmSync(snapshot);
    // Fail rather than replace a directory created by somebody else during the build.
    validateOutput(output, options.releaseDirectory);
    renameSync(staging, output);
    return { output, manifest };
  } finally {
    // Only this invocation's unique staging tree is owned and eligible for cleanup.
    rmSync(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const built = buildPackages(parseArguments(process.argv.slice(2)));
    console.log(`Built npm packages for Sodapop ${built.manifest.version} in ${built.output}`);
  } catch (error) {
    console.error(`sodapop npm packaging: ${error.message}`);
    process.exitCode = 1;
  }
}
