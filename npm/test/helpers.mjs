import assert from "node:assert/strict";
import { chmodSync, constants, copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { npmRoot, repositoryRoot, writeJSON } from "../scripts/build-packages.mjs";
import { temporaryRoot } from "../scripts/install-support.mjs";

const require = createRequire(import.meta.url);
export const { TARGETS, hashFile } = require("../packages/cli/lib/launcher.js");
export const host = TARGETS[`${process.platform}-${process.arch}`];
export const cliSource = path.join(npmRoot, "packages/cli");
const fixtures = path.join(path.dirname(fileURLToPath(import.meta.url)), "fixtures");
const versionPins = readFileSync(path.join(repositoryRoot, "internal/runtimebundle/version.go"), "utf8");
export const pins = {
  copilot_sdk_version: versionPins.match(/SDKVersion\s*=\s*"([^"]+)"/)[1],
  copilot_runtime_version: versionPins.match(/\bVersion\s*=\s*"([^"]+)"/)[1]
};

export function go(args, options = {}) {
  const [GOOS, GOARCH] = host.platform.split("/");
  const result = spawnSync("go", args, {
    cwd: repositoryRoot, encoding: "utf8", timeout: 180_000,
    env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", CGO_ENABLED: "0", GOFLAGS: "", GOOS, GOARCH, ...options.env }
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
}

export function buildRunner(root, target = host) {
  const binary = path.join(root, `${target.platform.replace("/", "-")}-${target.binary}`);
  const [GOOS, GOARCH] = target.platform.split("/");
  go(["build", "-trimpath", "-ldflags=-s -w", "-o", binary, path.join(fixtures, "runner.go")], { env: { GOOS, GOARCH } });
  return binary;
}

export function buildTools(root) {
  const suffix = process.platform === "win32" ? ".exe" : "";
  const helper = path.join(root, `releasectl${suffix}`);
  go(["build", "-o", helper, "./scripts/releasectl"]);
  const archive = path.join(root, `archive-fixture${suffix}`);
  go(["build", "-o", archive, path.join(fixtures, "archive.go")]);
  return { helper, archive };
}

export function writeArchive(root, version, target, binary, archiveTool, mutate = (entries) => entries) {
  const id = target.platform.replace("/", "-");
  const base = `sodapop-${version}-${id}`;
  const archive = `${base}${target.os === "win32" ? ".zip" : ".tar.gz"}`;
  const entries = mutate([
    { name: `${base}/${target.binary}`, file: binary, mode: "755" },
    { name: `${base}/README.md`, content: "Subprocess fixture, not native Sodapop" },
    { name: `${base}/LICENSE`, content: "fixture project license" },
    { name: `${base}/THIRD_PARTY_NOTICES.md`, content: "fixture upstream notices" },
    { name: `${base}/LICENSES/copilot-runtime.license`, content: "fixture runtime license" },
    { name: `${base}/LICENSES/example@v1.0.0/LICENSE`, content: "fixture module license" }
  ]);
  const input = path.join(root, `${id}-entries.json`);
  writeJSON(input, entries);
  const result = spawnSync(archiveTool, [input, path.join(root, archive)], { encoding: "utf8" });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  const archive_sha256 = hashFile(path.join(root, archive));
  writeFileSync(path.join(root, `${archive}.sha256`), `${archive_sha256}  ${archive}\n`);
  return { platform: target.platform, archive, archive_sha256, binary_sha256: hashFile(binary) };
}

export function releaseFixture(t, tools, runners, options = {}) {
  const root = temporaryRoot("release-fixture");
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const releases = path.join(root, "releases");
  mkdirSync(releases);
  const version = options.version || "1.2.3";
  const artifacts = [];
  for (const [platform, binary] of runners) {
    const target = Object.values(TARGETS).find((target) => target.platform === platform);
    artifacts.push(writeArchive(releases, version, target, binary, tools.archive, options.mutate));
  }
  const manifest = { schema_version: 1, version, commit: "a".repeat(40), ...pins, artifacts };
  const manifestFile = path.join(releases, `sodapop-${version}-manifest.json`);
  writeJSON(manifestFile, manifest);
  return { root, releaseDirectory: releases, version, manifest: manifestFile, data: manifest, output: path.join(root, "output"), helper: tools.helper };
}

export function launcherFixture(root, binary, target = host) {
  const cli = path.join(root, "node_modules/@sodapop/cli");
  const platformRoot = path.join(root, "node_modules", ...target.package.split("/"));
  for (const entry of ["bin/sodapop.js", "lib/launcher.js"]) {
    mkdirSync(path.dirname(path.join(cli, entry)), { recursive: true });
    copyFileSync(path.join(cliSource, entry), path.join(cli, entry));
  }
  mkdirSync(path.join(platformRoot, "bin"), { recursive: true });
  copyFileSync(binary, path.join(platformRoot, "bin", target.binary), constants.COPYFILE_FICLONE);
  chmodSync(path.join(platformRoot, "bin", target.binary), 0o755);
  const version = "1.2.3";
  const artifact = {
    platform: target.platform,
    archive: `sodapop-${version}-${target.platform.replace("/", "-")}${target.os === "win32" ? ".zip" : ".tar.gz"}`,
    archive_sha256: "b".repeat(64), binary_sha256: hashFile(binary)
  };
  const binding = { schema_version: 1, version, commit: "a".repeat(40), ...pins };
  const cliMetadata = {
    name: "@sodapop/cli", version, optionalDependencies: { [target.package]: version },
    sodapop: { ...binding, artifacts: [artifact] }
  };
  const metadata = {
    name: target.package, version, os: [target.os], cpu: [target.cpu],
    ...(target.os === "linux" ? { libc: ["glibc"] } : {}), sodapop: { ...binding, artifact }
  };
  writeJSON(path.join(cli, "package.json"), cliMetadata);
  writeJSON(path.join(platformRoot, "package.json"), metadata);
  return { cli, platformRoot, cliMetadata, metadata };
}
