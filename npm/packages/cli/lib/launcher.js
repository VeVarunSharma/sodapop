"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { createHash } = require("node:crypto");
const { spawn } = require("node:child_process");

const TARGETS = Object.freeze({
  "darwin-arm64": { package: "@sodapop-sh/darwin-arm64", platform: "darwin/arm64", os: "darwin", cpu: "arm64", binary: "sodapop" },
  "darwin-x64": { package: "@sodapop-sh/darwin-amd64", platform: "darwin/amd64", os: "darwin", cpu: "x64", binary: "sodapop" },
  "linux-arm64": { package: "@sodapop-sh/linux-arm64", platform: "linux/arm64", os: "linux", cpu: "arm64", binary: "sodapop" },
  "linux-x64": { package: "@sodapop-sh/linux-amd64", platform: "linux/amd64", os: "linux", cpu: "x64", binary: "sodapop" },
  "win32-arm64": { package: "@sodapop-sh/windows-arm64", platform: "windows/arm64", os: "win32", cpu: "arm64", binary: "sodapop.exe" },
  "win32-x64": { package: "@sodapop-sh/windows-amd64", platform: "windows/amd64", os: "win32", cpu: "x64", binary: "sodapop.exe" }
});
const PLATFORM_PACKAGES = Object.freeze(Object.fromEntries(Object.entries(TARGETS).map(([key, target]) => [key, target.package])));

function failure(code, message) {
  return Object.assign(new Error(`${code}: ${message}`), { code });
}

function selectTarget(platform = process.platform, arch = process.arch) {
  const target = TARGETS[`${platform}-${arch}`];
  if (!target) throw failure("SODAPOP_UNSUPPORTED_PLATFORM", `unsupported platform ${platform}/${arch}`);
  return target;
}

function platformPackage(platform, arch) {
  return selectTarget(platform, arch).package;
}

function requireGlibc(getReport = () => process.report.getReport()) {
  let report;
  try {
    report = getReport();
  } catch {
    throw failure("SODAPOP_AMBIGUOUS_LIBC", "cannot identify the Linux C library; a glibc-based Node.js runtime is required");
  }
  const glibc = typeof report?.header?.glibcVersionRuntime === "string" && /^\d+\.\d+/.test(report.header.glibcVersionRuntime);
  const musl = Array.isArray(report?.sharedObjects) && report.sharedObjects.some((name) => /(?:^|[/\\])(?:ld-musl-|libc\.musl-)/.test(name));
  if (musl && !glibc) throw failure("SODAPOP_UNSUPPORTED_LIBC", "Linux musl is unsupported; use glibc Linux");
  if (!glibc || musl) throw failure("SODAPOP_AMBIGUOUS_LIBC", "cannot unambiguously identify glibc Linux");
}

function hashFile(filename) {
  const hash = createHash("sha256");
  const fd = fs.openSync(filename, "r");
  try {
    const buffer = Buffer.alloc(1024 * 1024);
    let count;
    while ((count = fs.readSync(fd, buffer, 0, buffer.length, null)) !== 0) hash.update(buffer.subarray(0, count));
  } finally {
    fs.closeSync(fd);
  }
  return hash.digest("hex");
}

function resolvePayload(options = {}) {
  const packageRoot = options.packageRoot || path.resolve(__dirname, "..");
  const target = selectTarget(options.platform, options.arch);
  if (target.os === "linux") requireGlibc(options.getReport);
  const cli = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));
  const entries = cli.sodapop?.artifacts?.filter((artifact) => artifact.platform === target.platform);
  if (!entries || entries.length === 0 || !Object.hasOwn(cli.optionalDependencies || {}, target.package)) {
    throw failure("SODAPOP_UNAVAILABLE_PLATFORM", `${target.platform} is not declared by @sodapop-sh/cli@${cli.version}`);
  }
  if (entries.length !== 1) throw failure("SODAPOP_AMBIGUOUS_PLATFORM", `multiple payloads declared for ${target.platform}`);
  const artifact = entries[0];
  if (cli.sodapop.schema_version !== 1 || cli.sodapop.version !== cli.version ||
      cli.optionalDependencies[target.package] !== cli.version) {
    throw failure("SODAPOP_PAYLOAD_MISMATCH", "CLI release metadata or exact-version dependency is inconsistent");
  }

  let metadataPath;
  try {
    metadataPath = require.resolve(`${target.package}/package.json`, { paths: [packageRoot] });
  } catch (error) {
    if (error.code !== "MODULE_NOT_FOUND") throw error;
    throw failure("SODAPOP_MISSING_PAYLOAD", `native payload ${target.package}@${cli.version} is missing; reinstall @sodapop-sh/cli with optional dependencies enabled`);
  }
  const metadata = JSON.parse(fs.readFileSync(metadataPath, "utf8"));
  if (metadata.version !== cli.version) {
    throw failure("SODAPOP_VERSION_MISMATCH", `native payload version mismatch: @sodapop-sh/cli is ${cli.version}, ${target.package} is ${metadata.version}; reinstall @sodapop-sh/cli`);
  }
  const single = (value, expected) => Array.isArray(value) && value.length === 1 && value[0] === expected;
  const binding = metadata.sodapop;
  if (metadata.name !== target.package || !single(metadata.os, target.os) || !single(metadata.cpu, target.cpu) ||
      (target.os === "linux" && !single(metadata.libc, "glibc")) ||
      ["schema_version", "version", "commit", "copilot_sdk_version", "copilot_runtime_version"].some((key) => binding?.[key] !== cli.sodapop[key]) ||
      ["platform", "archive", "archive_sha256", "binary_sha256"].some((key) => binding?.artifact?.[key] !== artifact[key]) ||
      !/^[a-f0-9]{64}$/.test(artifact.binary_sha256)) {
    throw failure("SODAPOP_PAYLOAD_MISMATCH", `native payload metadata mismatch for ${target.package}`);
  }
  const binary = path.join(path.dirname(metadataPath), "bin", target.binary);
  try {
    if (!fs.lstatSync(binary).isFile()) throw failure("SODAPOP_PAYLOAD_MISMATCH", "native payload must be a regular file");
    fs.accessSync(binary, target.os === "win32" ? fs.constants.R_OK : fs.constants.X_OK);
  } catch (error) {
    if (error.code === "SODAPOP_PAYLOAD_MISMATCH") throw error;
    if (!["ENOENT", "EACCES", "EPERM"].includes(error.code)) throw error;
    throw failure("SODAPOP_MISSING_PAYLOAD", `${target.package}@${cli.version} does not contain executable bin/${target.binary}`);
  }
  if (hashFile(binary) !== artifact.binary_sha256) {
    throw failure("SODAPOP_PAYLOAD_MISMATCH", `native payload binary hash mismatch for ${target.package}; reinstall @sodapop-sh/cli`);
  }
  return binary;
}

async function main(args, options = {}) {
  const runtime = options.process || process;
  const errorOutput = options.errorOutput || ((message) => console.error(message));
  let binary;
  try {
    binary = resolvePayload(options);
  } catch (error) {
    errorOutput(`sodapop: ${error.message}`);
    return 1;
  }
  // Replacing Node preserves terminal job control and delivers each POSIX signal
  // once. Older Node and Windows use the inherited-console subprocess path.
  if (!options.spawn && runtime.platform !== "win32" && typeof runtime.execve === "function") {
    try {
      runtime.execve(binary, [binary, ...args], runtime.env);
    } catch (error) {
      errorOutput(`sodapop: SODAPOP_START_FAILED: ${error.message}`);
      return 1;
    }
  }
  return new Promise((resolve) => {
    let child;
    try {
      child = (options.spawn || spawn)(binary, args, { stdio: "inherit", env: runtime.env, cwd: runtime.cwd(), shell: false });
    } catch (error) {
      errorOutput(`sodapop: SODAPOP_START_FAILED: ${error.message}`);
      resolve(1);
      return;
    }
    const handlers = new Map();
    for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
      const handler = () => {
        // Windows broadcasts Ctrl+C to the inherited console. kill(SIGINT)
        // would forcibly terminate the child instead of letting it handle it.
        if (!(runtime.platform === "win32" && signal === "SIGINT")) child.kill(signal);
      };
      handlers.set(signal, handler);
      runtime.on(signal, handler);
    }
    const cleanup = () => {
      for (const [signal, handler] of handlers) runtime.removeListener(signal, handler);
    };
    let settled = false;
    child.once("error", (error) => {
      if (settled) return;
      settled = true;
      cleanup();
      errorOutput(`sodapop: SODAPOP_START_FAILED: ${error.message}`);
      resolve(1);
    });
    child.once("exit", (status, signal) => {
      if (settled) return;
      settled = true;
      cleanup();
      if (signal) {
        runtime.kill(runtime.pid, signal);
        resolve(1);
      } else resolve(status === null ? 1 : status);
    });
  });
}

module.exports = { TARGETS, PLATFORM_PACKAGES, hashFile, main, platformPackage, requireGlibc, resolvePayload };
