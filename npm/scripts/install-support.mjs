import assert from "node:assert/strict";
import { createRequire } from "node:module";
import {
  existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync,
  realpathSync, writeFileSync
} from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { validateVersion } from "./build-packages.mjs";

const require = createRequire(import.meta.url);
const { TARGETS, hashFile } = require("../packages/cli/lib/launcher.js");
export const publicRegistry = "https://registry.npmjs.org/";
const offlineRegistry = "http://127.0.0.1:9";

export function temporaryRoot(label) {
  return mkdtempSync(path.join(realpathSync(tmpdir()), `sodapop-${label}-`));
}

export function isolatedEnvironment(root) {
  const env = {};
  for (const key of ["PATH", "Path", "SystemRoot", "SYSTEMROOT", "WINDIR", "ComSpec", "PATHEXT"]) {
    if (process.env[key]) env[key] = process.env[key];
  }
  for (const name of ["home", "config", "cache", "data", "state", "tmp", "appdata", "localappdata"]) {
    mkdirSync(path.join(root, name), { recursive: true });
  }
  Object.assign(env, {
    HOME: path.join(root, "home"), USERPROFILE: path.join(root, "home"),
    XDG_CONFIG_HOME: path.join(root, "config"), XDG_CACHE_HOME: path.join(root, "cache"),
    XDG_DATA_HOME: path.join(root, "data"), XDG_STATE_HOME: path.join(root, "state"),
    APPDATA: path.join(root, "appdata"), LOCALAPPDATA: path.join(root, "localappdata"),
    TMPDIR: path.join(root, "tmp"), TMP: path.join(root, "tmp"), TEMP: path.join(root, "tmp"),
    SODAPOP_LIVE_QUALIFY: "0", SODAPOP_RUNTIME_SMOKE: "0",
    GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off",
    CI: "1", NO_COLOR: "1", NPM_CONFIG_UPDATE_NOTIFIER: "false"
  });
  return env;
}

function npmCLI() {
  const candidates = [
    process.env.npm_execpath,
    path.join(path.dirname(process.execPath), "node_modules/npm/bin/npm-cli.js"),
    path.resolve(path.dirname(process.execPath), "../lib/node_modules/npm/bin/npm-cli.js")
  ];
  for (const directory of (process.env.PATH || process.env.Path || "").split(path.delimiter)) {
    candidates.push(path.join(directory, "npm"), path.join(directory, "node_modules/npm/bin/npm-cli.js"));
  }
  for (const candidate of candidates) {
    if (!candidate || !existsSync(candidate)) continue;
    const real = realpathSync(candidate);
    if (path.basename(real) === "npm-cli.js") return real;
  }
  throw new Error("npm-cli.js was not found; run this command through npm with Node and npm installed");
}

export function createNpmSandbox(root = temporaryRoot("npm-install"), { registryInstall = false } = {}) {
  if (typeof registryInstall !== "boolean") throw new Error("registryInstall must be a boolean");
  const env = isolatedEnvironment(root);
  const npm = npmCLI();
  const work = path.join(root, "work");
  const prefix = path.join(root, "prefix");
  const tarballs = path.join(root, "tarballs");
  for (const directory of [work, prefix, tarballs]) mkdirSync(directory, { recursive: true });
  writeFileSync(path.join(work, "package.json"), '{"name":"sodapop-install-check","private":true}\n');
  const userconfig = path.join(root, "user.npmrc");
  const globalconfig = path.join(root, "global.npmrc");
  writeFileSync(userconfig, `@sodapop:registry=${registryInstall ? publicRegistry : offlineRegistry}\n`);
  writeFileSync(globalconfig, "");
  Object.assign(env, {
    NPM_CONFIG_USERCONFIG: userconfig, NPM_CONFIG_GLOBALCONFIG: globalconfig,
    NPM_CONFIG_CACHE: path.join(root, "npm-cache"), NPM_CONFIG_PREFIX: prefix,
    NPM_CONFIG_REGISTRY: registryInstall ? publicRegistry : offlineRegistry,
    NPM_CONFIG_OFFLINE: registryInstall ? "false" : "true",
    NPM_CONFIG_IGNORE_SCRIPTS: "true", NPM_CONFIG_AUDIT: "false", NPM_CONFIG_FUND: "false"
  });
  return { root, env, npm, work, prefix, tarballs, registryInstall };
}

export function runNpm(sandbox, args, { spawn: run = spawnSync } = {}) {
  const registry = sandbox.registryInstall ? publicRegistry : offlineRegistry;
  const network = sandbox.registryInstall ? ["--offline=false", "--prefer-online"] : ["--offline"];
  const result = run(process.execPath, [
    sandbox.npm, ...args, ...network, "--ignore-scripts", "--no-audit", "--no-fund",
    "--userconfig", sandbox.env.NPM_CONFIG_USERCONFIG,
    "--globalconfig", sandbox.env.NPM_CONFIG_GLOBALCONFIG,
    "--cache", sandbox.env.NPM_CONFIG_CACHE, "--registry", registry
  ], { cwd: sandbox.work, env: sandbox.env, encoding: "utf8", timeout: 120_000, maxBuffer: 8 * 1024 * 1024 });
  if (result.error || result.status !== 0) {
    const details = result.error?.message || [result.stdout, result.stderr].filter(Boolean).join("\n").trim() ||
      (result.signal ? `terminated by ${result.signal}` : `exit ${result.status}`);
    throw new Error(`npm ${args[0]} failed: ${details}`);
  }
  return result;
}

function licenseFiles(directory, prefix = "LICENSES") {
  const files = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const relative = `${prefix}/${entry.name}`;
    if (entry.isDirectory()) files.push(...licenseFiles(path.join(directory, entry.name), relative));
    else {
      assert.ok(entry.isFile(), `non-regular license ${relative}`);
      files.push(relative);
    }
  }
  return files;
}

export function packPackage(sandbox, directory) {
  assert.equal(sandbox.registryInstall, false, "public registry checks must not pack local payloads");
  const metadata = JSON.parse(readFileSync(path.join(directory, "package.json"), "utf8"));
  const expected = ["package.json", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"];
  if (metadata.name === "@sodapop/cli") expected.push("bin/sodapop.js", "lib/launcher.js");
  else {
    const target = Object.values(TARGETS).find((target) => target.package === metadata.name);
    assert.ok(target, `unexpected package ${metadata.name}`);
    expected.push(`bin/${target.binary}`, ...licenseFiles(path.join(directory, "LICENSES")));
    assert.notEqual(metadata.license, "MIT", "composite platform payload cannot be described as MIT-only");
  }
  assert.equal(metadata.scripts, undefined, "packages must not have lifecycle scripts");
  const result = runNpm(sandbox, ["pack", "--json", "--pack-destination", sandbox.tarballs, directory]);
  const packed = JSON.parse(result.stdout);
  assert.equal(packed.length, 1);
  assert.equal(packed[0].version, metadata.version);
  assert.deepEqual(packed[0].files.map((file) => file.path).sort(), expected.sort(), `npm pack file allowlist for ${metadata.name}`);
  const tarball = path.join(sandbox.tarballs, packed[0].filename);
  assert.ok(lstatSync(tarball).isFile(), "npm pack must create a real tarball");
  assert.ok(lstatSync(tarball).size > 0);
  return tarball;
}

export function globalPackage(sandbox, name) {
  return path.join(sandbox.prefix, ...(process.platform === "win32" ? [] : ["lib"]), "node_modules", ...name.split("/"));
}

export function globalShim(sandbox) {
  return path.join(sandbox.prefix, ...(process.platform === "win32" ? [] : ["bin"]), process.platform === "win32" ? "sodapop.cmd" : "sodapop");
}

export function runShim(sandbox, args, options = {}) {
  const shim = globalShim(sandbox);
  const spawnOptions = {
    cwd: sandbox.work, env: sandbox.env, encoding: "utf8", timeout: 70_000,
    ...options
  };
  if (process.platform !== "win32") return spawnSync(shim, args, spawnOptions);
  // Exercise npm's actual Windows shim, not its JS entry point. These smoke
  // arguments are deliberately restricted so cmd.exe cannot interpret data.
  const quote = (value) => {
    if (/["%!\r\n&|<>^]/.test(value)) throw new Error("Windows shim smoke arguments contain unsupported cmd.exe metacharacters");
    return `"${value}"`;
  };
  return spawnSync(sandbox.env.ComSpec || "cmd.exe", ["/d", "/s", "/c",
    `"${[shim, ...args].map(quote).join(" ")}"`], { ...spawnOptions, windowsVerbatimArguments: true });
}

export function assertSuccess(result, label) {
  assert.ifError(result.error);
  assert.equal(result.status, 0, `${label}: ${result.stdout}\n${result.stderr}`);
}

export function installLocal(sandbox, cliTarball, hostTarball) {
  assert.equal(sandbox.registryInstall, false, "local tarballs must not seed a registry installation");
  runNpm(sandbox, ["install", "--global", "--prefix", sandbox.prefix, hostTarball, cliTarball]);
  assert.ok(existsSync(globalShim(sandbox)), "npm global shim is missing");
}

export function installRegistry(sandbox, version, { omitOptional = false, ...dependencies } = {}) {
  assert.equal(sandbox.registryInstall, true, "public registry installation requires explicit registry mode");
  validateVersion(version);
  runNpm(sandbox, [
    "install", "--global", "--prefix", sandbox.prefix,
    omitOptional ? "--omit=optional" : "--include=optional", `@sodapop/cli@${version}`
  ], dependencies);
  assert.ok(existsSync(globalShim(sandbox)), "npm global shim is missing");
}

function assertWithinPrefix(sandbox, filename) {
  const relative = path.relative(realpathSync(sandbox.prefix), realpathSync(filename));
  assert.ok(relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative),
    `installed payload resolved outside the isolated npm prefix: ${filename}`);
}

export function assertInstalledHash(sandbox, manifest, target) {
  const entry = manifest.artifacts.find((artifact) => artifact.platform === target.platform);
  assert.ok(entry, `manifest does not declare host ${target.platform}`);
  const cliPath = path.join(globalPackage(sandbox, "@sodapop/cli"), "package.json");
  assertWithinPrefix(sandbox, cliPath);
  const cli = JSON.parse(readFileSync(cliPath, "utf8"));
  assert.equal(cli.name, "@sodapop/cli");
  assert.equal(cli.version, manifest.version);
  const resolver = createRequire(cliPath);
  let metadataPath;
  try {
    metadataPath = resolver.resolve(`${target.package}/package.json`);
    assertWithinPrefix(sandbox, metadataPath);
  } catch (error) {
    if (!["MODULE_NOT_FOUND", "ENOENT"].includes(error.code)) throw error;
    throw Object.assign(new Error(`installed native payload ${target.package}@${manifest.version} is missing`),
      { code: "SODAPOP_MISSING_PAYLOAD", cause: error });
  }
  const metadata = JSON.parse(readFileSync(metadataPath, "utf8"));
  assert.equal(metadata.name, target.package);
  assert.equal(metadata.version, manifest.version);
  const binary = path.join(path.dirname(metadataPath), "bin", target.binary);
  assertWithinPrefix(sandbox, binary);
  assert.ok(lstatSync(binary).isFile(), "installed native payload must be a regular file");
  assert.equal(hashFile(binary), entry.binary_sha256, "installed native binary does not match the verified release manifest");
  return path.dirname(metadataPath);
}

export function writeSentinels(sandbox) {
  const files = [
    path.join(sandbox.env.HOME, ".profile"), path.join(sandbox.env.HOME, ".bashrc"),
    path.join(sandbox.env.HOME, ".zshrc"), path.join(sandbox.env.HOME, ".copilot", "keep"),
    path.join(sandbox.env.XDG_CONFIG_HOME, "sodapop", "preferences.sentinel"),
    path.join(sandbox.env.XDG_STATE_HOME, "sodapop", "session.sentinel"),
    path.join(sandbox.env.XDG_CACHE_HOME, "sodapop", "runtime.sentinel"),
    path.join(sandbox.env.LOCALAPPDATA, "sodapop", "state.sentinel"),
    path.join(sandbox.env.USERPROFILE, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
    path.join(sandbox.prefix, "unrelated.sentinel")
  ];
  for (const file of files) {
    mkdirSync(path.dirname(file), { recursive: true });
    writeFileSync(file, "preserve exactly\n");
  }
  return () => {
    for (const file of files) assert.equal(readFileSync(file, "utf8"), "preserve exactly\n", `modified unowned state ${file}`);
  };
}

export function uninstallLocal(sandbox, target) {
  runNpm(sandbox, ["uninstall", "--global", "--prefix", sandbox.prefix, "@sodapop/cli", target.package]);
  assert.equal(existsSync(globalShim(sandbox)), false);
  assert.equal(existsSync(globalPackage(sandbox, "@sodapop/cli")), false);
  assert.equal(existsSync(globalPackage(sandbox, target.package)), false);
}
