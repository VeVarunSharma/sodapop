#!/usr/bin/env node

import assert from "node:assert/strict";
import { copyFileSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { buildPackages, readManifest, releaseControl } from "./build-packages.mjs";
import {
  assertInstalledHash, assertSuccess, createNpmSandbox, globalPackage, installLocal, installRegistry, packPackage,
  runNpm, runShim, temporaryRoot, uninstallLocal, writeSentinels
} from "./install-support.mjs";

const require = createRequire(import.meta.url);
const { TARGETS, requireGlibc } = require("../packages/cli/lib/launcher.js");
export const usage = "npm run test:install -- --release-dir DIR --manifest FILE [--registry-install] [--previous-release-dir DIR --previous-manifest FILE]";
export const upgradeNotRun = "Upgrade NOT RUN: no previous release inputs supplied; this run does not establish upgrade readiness.";

export function parseInstallArguments(args) {
  const names = {
    "--release-dir": "releaseDirectory", "--manifest": "manifest",
    "--previous-release-dir": "previousReleaseDirectory", "--previous-manifest": "previousManifest"
  };
  const options = {};
  for (let i = 0; i < args.length; i += 1) {
    if (args[i] === "--registry-install") {
      if (options.registryInstall) throw new Error(`repeated argument --registry-install\n${usage}`);
      options.registryInstall = true;
      continue;
    }
    const key = names[args[i]];
    if (!key || options[key] || !args[i + 1] || args[i + 1].startsWith("--")) throw new Error(`invalid argument ${args[i]}\n${usage}`);
    options[key] = path.resolve(args[i + 1]);
    i += 1;
  }
  if (!options.releaseDirectory || !options.manifest) {
    throw new Error(`--release-dir and --manifest are required\n${usage}`);
  }
  if (Boolean(options.previousReleaseDirectory) !== Boolean(options.previousManifest)) {
    throw new Error(`--previous-release-dir and --previous-manifest must be supplied together\n${usage}`);
  }
  return options;
}

function compareVersions(left, right) {
  const parts = (value) => {
    const separator = value.indexOf("-");
    return separator === -1 ? [value, undefined] : [value.slice(0, separator), value.slice(separator + 1)];
  };
  const [leftCore, leftPre] = parts(left);
  const [rightCore, rightPre] = parts(right);
  const a = leftCore.split(".").map(BigInt);
  const b = rightCore.split(".").map(BigInt);
  for (let i = 0; i < 3; i += 1) if (a[i] !== b[i]) return a[i] > b[i] ? 1 : -1;
  if (leftPre === rightPre) return 0;
  if (leftPre === undefined) return 1;
  if (rightPre === undefined) return -1;
  const x = leftPre.split(".");
  const y = rightPre.split(".");
  for (let i = 0; i < Math.max(x.length, y.length); i += 1) {
    if (x[i] === undefined) return -1;
    if (y[i] === undefined) return 1;
    if (x[i] === y[i]) continue;
    const numericX = /^\d+$/.test(x[i]);
    const numericY = /^\d+$/.test(y[i]);
    if (numericX && numericY) return BigInt(x[i]) > BigInt(y[i]) ? 1 : -1;
    if (numericX !== numericY) return numericX ? -1 : 1;
    return x[i] > y[i] ? 1 : -1;
  }
  return 0;
}

function loadRelease(releaseDirectory, filename) {
  const bytes = readFileSync(filename);
  const version = JSON.parse(bytes).version;
  const release = { releaseDirectory, manifest: filename, version };
  return { ...release, bytes, metadata: readManifest(release, bytes) };
}

export function selectTargetManifest(manifest, platform) {
  const artifacts = manifest.artifacts.filter((artifact) => artifact.platform === platform);
  if (artifacts.length !== 1) {
    throw new Error(`manifest must declare exactly one ${platform} artifact`);
  }
  return { ...manifest, artifacts };
}

function nativeEnvironment(sandbox) {
  const bin = path.join(sandbox.root, "runtime-bin");
  mkdirSync(bin);
  const node = path.join(bin, path.basename(process.execPath));
  if (process.platform === "win32") copyFileSync(process.execPath, node);
  else symlinkSync(process.execPath, node);
  // No Go, installed Copilot, npm lifecycle hook, or first-run downloader is
  // available on the command's PATH. npm itself is invoked by absolute JS path.
  const env = { ...sandbox.env, PATH: bin };
  delete env.Path;
  return env;
}

function smoke(sandbox, manifest, target, env) {
  assertInstalledHash(sandbox, manifest, target);
  const help = runShim(sandbox, ["--help"], { env });
  assertSuccess(help, "native --help");
  assert.match(help.stdout, /Usage: sodapop/);
  const version = runShim(sandbox, ["--version"], { env });
  assertSuccess(version, "native --version");
  assert.equal(version.stdout.replace(/\r\n/g, "\n").trim(),
    `sodapop ${manifest.version}\nCopilot SDK ${manifest.copilot_sdk_version} / runtime ${manifest.copilot_runtime_version}`);
  for (const phase of ["first", "second"]) {
    const runtime = runShim(sandbox, ["--check-runtime"], { env });
    assertSuccess(runtime, `${phase} native --check-runtime`);
    assert.ok(runtime.stdout.includes(`Copilot ${manifest.copilot_runtime_version} (protocol `), runtime.stdout);
  }
  const invalid = runShim(sandbox, ["--sodapop-invalid-fixture-flag", "value with spaces"], { env });
  assert.ifError(invalid.error);
  assert.notEqual(invalid.status, 0, "native flag errors must not become success");
  assert.match(`${invalid.stdout}\n${invalid.stderr}`, /flag provided but not defined/);
  assertInstalledHash(sandbox, manifest, target);
}

export function testNativeInstall(options) {
  const target = TARGETS[`${process.platform}-${process.arch}`];
  assert.ok(target, `unsupported host ${process.platform}/${process.arch}`);
  if (target.os === "linux") requireGlibc();
  const current = loadRelease(options.releaseDirectory, options.manifest);
  const previous = options.previousManifest && loadRelease(options.previousReleaseDirectory, options.previousManifest);
  const currentManifest = current.metadata;
  const previousManifest = previous && previous.metadata;
  if (previousManifest) {
    assert.ok(compareVersions(currentManifest.version, previousManifest.version) > 0, "current version must be newer than the real previous release");
  }
  for (const manifest of previousManifest ? [currentManifest, previousManifest] : [currentManifest]) {
    assert.ok(manifest.artifacts.some((artifact) => artifact.platform === target.platform), `manifest ${manifest.version} does not declare host ${target.platform}`);
  }
  if (!previous) console.log(upgradeNotRun);
  const root = temporaryRoot("native-npm-e2e");
  try {
    const sandbox = createNpmSandbox(path.join(root, "isolated"), { registryInstall: options.registryInstall });
    const preserve = writeSentinels(sandbox);
    const env = nativeEnvironment(sandbox);
    const prepare = (release, name) => {
      const snapshot = path.join(root, `${name}-manifest.json`);
      const bytes = options.registryInstall
        ? release.bytes
        : Buffer.from(`${JSON.stringify(selectTargetManifest(release.metadata, target.platform), null, 2)}\n`);
      writeFileSync(snapshot, bytes);
      if (options.registryInstall) {
        releaseControl(["verify", "--dir", release.releaseDirectory, "--manifest", snapshot,
          "--platform", target.platform]);
        return { manifest: release.metadata };
      }
      const built = buildPackages({ ...release, manifest: snapshot, output: path.join(root, name) });
      const cli = packPackage(sandbox, path.join(built.output, "cli"));
      const host = packPackage(sandbox, path.join(built.output, "platforms", target.platform.replace("/", "-")));
      return { ...built, cli, host };
    };
    const install = (release) => {
      if (options.registryInstall) installRegistry(sandbox, release.manifest.version);
      else installLocal(sandbox, release.cli, release.host);
    };
    const older = previous && prepare(previous, "previous-packages");
    const newer = prepare(current, "current-packages");
    if (older) {
      install(older);
      smoke(sandbox, older.manifest, target, env);
      preserve();
    }
    install(newer);
    smoke(sandbox, newer.manifest, target, env);
    preserve();
    install(newer);
    smoke(sandbox, newer.manifest, target, env);
    preserve();
    uninstallLocal(sandbox, target);
    preserve();
    if (options.registryInstall) {
      installRegistry(sandbox, newer.manifest.version);
      rmSync(globalPackage(sandbox, target.package), { recursive: true, force: true });
      // Never execute registry-supplied JS when the native payload cannot first
      // be checked against the trusted archive's manifest. Remove it explicitly:
      // npm may retain platform optional dependencies despite --omit=optional.
      assert.throws(() => assertInstalledHash(sandbox, newer.manifest, target), { code: "SODAPOP_MISSING_PAYLOAD" });
    } else {
      runNpm(sandbox, ["install", "--global", "--prefix", sandbox.prefix, "--omit=optional", newer.cli]);
      const missing = runShim(sandbox, ["--version"], { env });
      assert.equal(missing.status, 1);
      assert.match(missing.stderr, /SODAPOP_MISSING_PAYLOAD/);
    }
    uninstallLocal(sandbox, target);
    preserve();
    const upgrade = older ? `, ${older.manifest.version} -> ${newer.manifest.version} upgrade` : "";
    const mode = options.registryInstall ? "public-registry" : "local-tarball";
    console.log(`Native npm ${mode} install${upgrade}, reinstall, and uninstall passed for ${target.platform}. No registry publication was performed.`);
    if (!older) console.log(upgradeNotRun);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    testNativeInstall(parseInstallArguments(process.argv.slice(2)));
  } catch (error) {
    console.error(`sodapop npm native install: ${error.message}`);
    process.exitCode = 1;
  }
}
