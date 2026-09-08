import assert from "node:assert/strict";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";
import test, { before, after } from "node:test";
import { gunzipSync } from "node:zlib";
import { buildPackages, npmRoot, parseArguments, readManifest, validateOutput, writeJSON } from "../scripts/build-packages.mjs";
import {
  assertInstalledHash, assertSuccess, createNpmSandbox, globalPackage, installLocal,
  packPackage, runNpm, runShim, temporaryRoot, uninstallLocal, writeSentinels
} from "../scripts/install-support.mjs";
import { buildRunner, buildTools, hashFile, host, releaseFixture, TARGETS } from "./helpers.mjs";

const root = temporaryRoot("build-tests");
let tools;
const runners = new Map();
before(() => {
  tools = buildTools(root);
  for (const target of Object.values(TARGETS)) runners.set(target.platform, buildRunner(root, target));
});
after(() => rmSync(root, { recursive: true, force: true }));

function metadata(directory) {
  return JSON.parse(readFileSync(path.join(directory, "package.json"), "utf8"));
}

const websiteFixtureFiles = [
  "site/AGENTS.md", "site/package.json", "site/package-lock.json", "site/astro.config.mjs",
  "site/src/pages/index.astro", "site/src/content/docs/docs/getting-started.md",
  "site/node_modules/astro/package.json", "site/node_modules/@vendor/dependency/AGENTS.md",
  "site/dist/index.html", "site/dist/_astro/client.js", "site/.astro/content.d.ts",
  "site/.generated/release-catalog.json", "site/public/assets/mascot.png",
  "site/.env", "site/.env.local", "site/.sodapop.env", "site/.npmrc",
  "site/dist/.env", "site/.generated/.env.local",
  "node_modules/frontend-dependency/AGENTS.md", "dist/site/index.html",
  ".env", ".env.local", ".sodapop.env", ".npmrc"
];
const websiteMarker = "WEBSITE_FIXTURE_ONLY_MUST_NOT_SHIP";

function writeWebsiteFixtures(directory) {
  const files = new Map();
  for (const relative of websiteFixtureFiles) {
    const filename = path.join(directory, relative);
    const marker = `${websiteMarker} ${relative}`;
    const content = relative.endsWith("package.json")
      ? JSON.stringify({ private: true, description: marker })
      : relative.endsWith(".npmrc") ? `# ${marker}\n` : `${marker}\n`;
    mkdirSync(path.dirname(filename), { recursive: true });
    writeFileSync(filename, content, { mode: 0o600 });
    files.set(filename, content);
  }
  return files;
}

function assertWebsitePayloadIsolation(t, includeWindows) {
  const selected = new Map([...runners].filter(([platform]) => includeWindows || platform !== "windows/amd64"));
  assert.equal(selected.size, includeWindows ? 5 : 4);
  const f = releaseFixture(t, tools, selected);
  const repository = path.join(f.root, "repository");
  const sources = [
    "npm/scripts/build-packages.mjs", "npm/packages/cli/package.json",
    "npm/packages/cli/bin/sodapop.js", "npm/packages/cli/lib/launcher.js",
    "npm/packages/cli/README.md", "npm/packages/platform-README.md",
    "LICENSE", "THIRD_PARTY_NOTICES.md", "go.mod", "internal/runtimebundle/version.go",
    ...[...selected.keys()].map((platform) => `npm/packages/${platform.replace("/", "-")}/package.json`)
  ];
  for (const relative of sources) {
    mkdirSync(path.dirname(path.join(repository, relative)), { recursive: true });
    copyFileSync(path.join(path.dirname(npmRoot), relative), path.join(repository, relative));
  }
  const protectedFiles = new Map(writeWebsiteFixtures(repository));
  const ids = ["cli", ...[...selected.keys()].map((platform) => platform.replace("/", "-"))];
  for (const id of ids) {
    for (const [filename, content] of writeWebsiteFixtures(path.join(repository, "npm/packages", id))) {
      protectedFiles.set(filename, content);
    }
  }

  const result = spawnSync(process.execPath, [
    path.join(repository, "npm/scripts/build-packages.mjs"),
    "--version", f.version, "--release-dir", f.releaseDirectory,
    "--manifest", f.manifest, "--platforms", [...selected.keys()].join(","), "--output", f.output
  ], {
    cwd: f.root, env: { ...process.env, SODAPOP_RELEASECTL: tools.helper },
    encoding: "utf8", timeout: 120_000
  });
  assertSuccess(result, "isolated website fixture build");
  const cli = metadata(path.join(f.output, "cli"));
  const expectedDependencies = Object.values(TARGETS)
    .filter((target) => selected.has(target.platform)).map((target) => target.package).sort();
  assert.deepEqual(Object.keys(cli.optionalDependencies).sort(), expectedDependencies);
  assert.equal(cli.optionalDependencies["@sodapop-sh/windows-amd64"], includeWindows ? f.version : undefined);
  assert.deepEqual(cli.sodapop, f.data);
  const sandbox = createNpmSandbox(path.join(f.root, "sandbox"));
  for (const id of ids) {
    const directory = id === "cli" ? path.join(f.output, "cli") : path.join(f.output, "platforms", id);
    for (const relative of websiteFixtureFiles) {
      assert.equal(existsSync(path.join(directory, relative)), false, `builder copied excluded ${id}/${relative}`);
    }
    if (id !== "cli") {
      const artifact = f.data.artifacts.find((entry) => entry.platform.replace("/", "-") === id);
      assert.ok(artifact, `missing release artifact for ${id}`);
      const target = Object.values(TARGETS).find((entry) => entry.platform === artifact.platform);
      assert.ok(target, `missing target metadata for ${id}`);
      const extension = target.os === "win32" ? ".zip" : ".tar.gz";
      assert.equal(artifact.archive, `sodapop-${f.version}-${id}${extension}`);
      assert.deepEqual(metadata(directory).sodapop.artifact, artifact);
      assert.equal(hashFile(path.join(directory, "bin", target.binary)), artifact.binary_sha256);
    }
    writeWebsiteFixtures(directory);
    const tarball = packPackage(sandbox, directory);
    assert.equal(gunzipSync(readFileSync(tarball)).includes(Buffer.from(websiteMarker)), false,
      `npm payload included website fixture content for ${id}`);
  }
  for (const [filename, content] of protectedFiles) {
    assert.equal(readFileSync(filename, "utf8"), content, `packaging changed excluded source ${filename}`);
  }
}

for (const [platformSet, includeWindows] of [["four-Unix", false], ["five-platform", true]]) {
  test(`website fixtures stay out of ${platformSet} npm build and pack payloads`, (t) => {
    assertWebsitePayloadIsolation(t, includeWindows);
  });
}

test("shared verification/extraction builds declared platforms and exact pinned metadata", (t) => {
  const f = releaseFixture(t, tools, runners);
  buildPackages(f);
  const cli = metadata(path.join(f.output, "cli"));
  assert.equal(cli.version, f.version);
  assert.equal(cli.private, undefined);
  assert.deepEqual(cli.sodapop, f.data);
  assert.equal(Object.keys(cli.optionalDependencies).length, 5);
  for (const target of Object.values(TARGETS)) {
    assert.equal(cli.optionalDependencies[target.package], f.version);
    const directory = path.join(f.output, "platforms", target.platform.replace("/", "-"));
    const platform = metadata(directory);
    assert.equal(platform.version, f.version);
    assert.equal(platform.private, undefined);
    assert.equal(hashFile(path.join(directory, "bin", target.binary)), hashFile(runners.get(target.platform)));
    if (target.os === "linux") assert.deepEqual(platform.libc, ["glibc"]);
    assert.notEqual(platform.license, "MIT");
    assert.equal(readFileSync(path.join(directory, "LICENSES/copilot-runtime.license"), "utf8"), "fixture runtime license");
  }
});

test("four-Unix and explicit host-only manifests never advertise absent payloads", (t) => {
  for (const selected of [
    new Map([...runners].filter(([platform]) => platform !== "windows/amd64")),
    new Map([[host.platform, runners.get(host.platform)]])
  ]) {
    const f = releaseFixture(t, tools, selected);
    buildPackages({ ...f, platforms: [...selected.keys()].join(",") });
    const cli = metadata(path.join(f.output, "cli"));
    assert.equal(Object.keys(cli.optionalDependencies).length, selected.size);
    assert.equal(Object.keys(cli.optionalDependencies).includes("@sodapop-sh/windows-amd64"), host.os === "win32" && selected.size === 1);
  }
});

test("version, pins, platform declarations, reused binaries and hashes fail closed", (t) => {
  const f = releaseFixture(t, tools, runners);
  for (const version of ["dev", "latest", "v1.2.3", "1.2", "1.2.3-01", "1.2.3+commit.abc"]) {
    assert.throws(() => readManifest({ ...f, version }), /valid semantic version without build metadata/);
  }
  const variants = [
    { ...f.data, schema_version: 2 }, { ...f.data, version: "2.3.4" }, { ...f.data, commit: "abc" },
    { ...f.data, copilot_sdk_version: "0.0.0" }, { ...f.data, copilot_runtime_version: "0.0.0" },
    { ...f.data, artifacts: [] }, { ...f.data, artifacts: [...f.data.artifacts, f.data.artifacts[0]] },
    { ...f.data, artifacts: [{ ...f.data.artifacts[0], platform: "linux/ppc64" }] },
    { ...f.data, artifacts: [{ ...f.data.artifacts[0], archive: "../bad.tar.gz" }] },
    { ...f.data, artifacts: [{ ...f.data.artifacts[0], archive_sha256: "bad" }] },
    { ...f.data, artifacts: f.data.artifacts.map((artifact) => ({ ...artifact, binary_sha256: "b".repeat(64) })) }
  ];
  for (const data of variants) {
    writeJSON(f.manifest, data);
    assert.throws(() => readManifest(f), /manifest/);
  }
  writeJSON(f.manifest, f.data);
  assert.throws(() => readManifest({ ...f, platforms: host.platform }), /exactly match/);
  assert.throws(() => parseArguments(["--version", "1.2.3"]), /--manifest is required/);
  assert.throws(() => parseArguments(["--manifest", "m", "--version", "1.2.3", "--version", "1.2.3"]), /repeated/);
});

test("archive corruption, binary mismatch, traversal and symlink extraction failures leave no output", (t) => {
  const selected = new Map([[host.platform, runners.get(host.platform)]]);
  for (const kind of ["archive", "binary", "traversal", "symlink", "missing-license"]) {
    const mutate = (entries) => {
      if (kind === "traversal") entries.push({ name: "../escaped", content: "escape" });
      if (kind === "symlink") entries.push({ name: entries[0].name.replace(host.binary, "LICENSES/link"), content: "/etc/passwd", type: "symlink" });
      if (kind === "missing-license") return entries.filter((entry) => !entry.name.endsWith("/copilot-runtime.license"));
      return entries;
    };
    const f = releaseFixture(t, tools, selected, { mutate });
    if (kind === "archive") writeFileSync(path.join(f.releaseDirectory, f.data.artifacts[0].archive), "corrupt");
    if (kind === "binary") {
      f.data.artifacts[0].binary_sha256 = "0".repeat(64);
      writeJSON(f.manifest, f.data);
    }
    assert.throws(() => buildPackages(f), /releasectl|license/i, kind);
    assert.equal(existsSync(f.output), false);
    assert.equal(existsSync(path.join(f.root, "escaped")), false);
  }
});

test("output rejects existing directories, dangerous ancestors and symlinks without deletion", (t) => {
  const f = releaseFixture(t, tools, new Map([[host.platform, runners.get(host.platform)]]));
  mkdirSync(f.output);
  writeFileSync(path.join(f.output, "keep"), "unowned");
  assert.throws(() => buildPackages(f), /already exists/);
  assert.equal(readFileSync(path.join(f.output, "keep"), "utf8"), "unowned");
  for (const output of [path.parse(f.root).root, npmRoot, path.dirname(npmRoot), f.releaseDirectory, path.join(f.releaseDirectory, "nested")]) {
    assert.throws(() => validateOutput(output, f.releaseDirectory), /scoped build directory/);
  }
  const link = path.join(f.root, "link");
  symlinkSync(f.output, link, process.platform === "win32" ? "junction" : "dir");
  assert.throws(() => validateOutput(link, f.releaseDirectory), /symlinks/);
  assert.throws(() => validateOutput(path.join(link, "output"), f.releaseDirectory), /symlinks/);
  assert.equal(readFileSync(path.join(f.output, "keep"), "utf8"), "unowned");
});

test("builder CLI uses repository-root helper cwd even when invoked outside npm", (t) => {
  const f = releaseFixture(t, tools, new Map([[host.platform, runners.get(host.platform)]]));
  const result = spawnSync(process.execPath, [
    path.join(npmRoot, "scripts/build-packages.mjs"), "--version", f.version,
    "--release-dir", f.releaseDirectory, "--manifest", f.manifest, "--output", f.output
  ], { cwd: f.root, env: { ...process.env, SODAPOP_RELEASECTL: tools.helper }, encoding: "utf8" });
  assertSuccess(result, "builder command");
});

test("real npm pack and offline global install/reinstall/upgrade/uninstall use generated shims", (t) => {
  // A complete release still installs using only the CLI and host tarballs.
  const f = releaseFixture(t, tools, runners);
  const previous = releaseFixture(t, tools, runners, { version: "1.2.2" });
  buildPackages(f);
  buildPackages(previous);
  const sandbox = createNpmSandbox(path.join(f.root, "sandbox"));
  const preserve = writeSentinels(sandbox);
  const cliDirectory = path.join(f.output, "cli");
  writeFileSync(path.join(cliDirectory, "unexpected-secret.txt"), "do not pack");
  writeFileSync(path.join(cliDirectory, "lib/unexpected.js"), "do not pack");
  const cli = packPackage(sandbox, cliDirectory);
  const platform = packPackage(sandbox, path.join(f.output, "platforms", host.platform.replace("/", "-")));
  const previousCLI = packPackage(sandbox, path.join(previous.output, "cli"));
  const previousPlatform = packPackage(sandbox, path.join(previous.output, "platforms", host.platform.replace("/", "-")));

  installLocal(sandbox, previousCLI, previousPlatform);
  assertInstalledHash(sandbox, previous.data, host);
  installLocal(sandbox, cli, platform);
  const installed = assertInstalledHash(sandbox, f.data, host);
  const args = ["--exit", "29", "value with spaces", ""];
  const result = runShim(sandbox, args, { input: "global stdin\n" });
  assert.ifError(result.error);
  assert.equal(result.status, 29, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { fixture: true, args, cwd: sandbox.work, stdin: "global stdin\n" });
  assert.match(result.stderr, /fixture-stderr/);
  preserve();

  const installedJSON = path.join(installed, "package.json");
  const original = readFileSync(installedJSON);
  writeJSON(installedJSON, { ...JSON.parse(original), version: "9.9.9" });
  assert.match(runShim(sandbox, []).stderr, /SODAPOP_VERSION_MISMATCH/);
  writeFileSync(installedJSON, original);
  writeFileSync(path.join(installed, "bin", host.binary), "tampered payload");
  assert.match(runShim(sandbox, []).stderr, /SODAPOP_PAYLOAD_MISMATCH/);
  uninstallLocal(sandbox, host);
  preserve();
  installLocal(sandbox, cli, platform);
  assertInstalledHash(sandbox, f.data, host);
  assertSuccess(runShim(sandbox, ["value with spaces"], { input: "" }), "reinstall fixture");
  uninstallLocal(sandbox, host);
  preserve();

  runNpm(sandbox, ["install", "--global", "--prefix", sandbox.prefix, "--omit=optional", cli]);
  assert.ok(existsSync(globalPackage(sandbox, "@sodapop-sh/cli")));
  const missing = runShim(sandbox, []);
  assert.equal(missing.status, 1);
  assert.match(missing.stderr, /SODAPOP_MISSING_PAYLOAD/);
  assert.match(missing.stderr, /optional dependencies enabled/);
  uninstallLocal(sandbox, host);
  preserve();
});

test("real pack checks all five platform file allowlists without cross-platform execution", (t) => {
  const f = releaseFixture(t, tools, runners);
  buildPackages(f);
  const sandbox = createNpmSandbox(path.join(f.root, "sandbox"));
  for (const target of Object.values(TARGETS)) {
    const directory = path.join(f.output, "platforms", target.platform.replace("/", "-"));
    writeFileSync(path.join(directory, "bin/unexpected"), "do not pack");
    packPackage(sandbox, directory);
  }
});
