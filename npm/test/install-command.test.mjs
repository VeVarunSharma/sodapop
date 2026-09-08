import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { parseInstallArguments, upgradeNotRun } from "../scripts/test-install.mjs";
import {
  assertInstalledHash, createNpmSandbox, globalPackage, globalShim, installLocal,
  installRegistry, packPackage, publicRegistry, runNpm, temporaryRoot
} from "../scripts/install-support.mjs";
import { writeJSON } from "../scripts/build-packages.mjs";

const require = createRequire(import.meta.url);
const { TARGETS } = require("../packages/cli/lib/launcher.js");
const host = TARGETS[`${process.platform}-${process.arch}`];

function sandboxFixture(t, registryInstall = false) {
  const root = temporaryRoot("registry-mode-unit");
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return createNpmSandbox(path.join(root, "isolated"), { registryInstall });
}

test("native install command requires current inputs and an optional complete previous pair", () => {
  for (const args of [
    [], ["--release-dir", "current"], ["--manifest", "current.json"],
    ["--release-dir", "current", "--manifest", "current.json", "--skip-upgrade", "yes"],
    ["--release-dir", "current", "--release-dir", "again"],
    ["--release-dir", "current", "--manifest", "current.json", "--previous-release-dir", "previous"],
    ["--release-dir", "current", "--manifest", "current.json", "--previous-manifest", "previous.json"]
  ]) {
    assert.throws(() => parseInstallArguments(args), /required|invalid argument|supplied together/);
  }
  assert.deepEqual(parseInstallArguments(["--release-dir", "current", "--manifest", "current.json"]), {
    releaseDirectory: path.resolve("current"), manifest: path.resolve("current.json")
  });
  assert.match(upgradeNotRun, /Upgrade NOT RUN/);
  assert.match(upgradeNotRun, /does not establish upgrade readiness/);
  assert.deepEqual(parseInstallArguments([
    "--release-dir", "current", "--manifest", "current.json",
    "--previous-release-dir", "previous", "--previous-manifest", "previous.json"
  ]), {
    releaseDirectory: path.resolve("current"), manifest: path.resolve("current.json"),
    previousReleaseDirectory: path.resolve("previous"), previousManifest: path.resolve("previous.json")
  });
});

test("public registry mode is explicit, position independent, and cannot override registry URL", () => {
  const release = ["--release-dir", "current", "--manifest", "current.json"];
  for (const args of [["--registry-install", ...release], [...release, "--registry-install"]]) {
    assert.deepEqual(parseInstallArguments(args), {
      releaseDirectory: path.resolve("current"), manifest: path.resolve("current.json"), registryInstall: true
    });
  }
  for (const args of [
    [...release, "--registry-install", "--registry-install"],
    [...release, "--registry-install", "false"],
    [...release, "--registry-install=false"],
    [...release, "--registry", "https://example.invalid"]
  ]) assert.throws(() => parseInstallArguments(args), /invalid|repeated argument/);
});

test("registry mode starts with an isolated empty prefix/cache and forces unauthenticated public config", (t) => {
  const sandbox = sandboxFixture(t, true);
  assert.deepEqual(readdirSync(sandbox.prefix), []);
  assert.deepEqual(readdirSync(sandbox.tarballs), []);
  assert.equal(existsSync(sandbox.env.NPM_CONFIG_CACHE), false);
  assert.equal(sandbox.env.NPM_CONFIG_REGISTRY, publicRegistry);
  assert.equal(sandbox.env.NPM_CONFIG_OFFLINE, "false");
  assert.equal(sandbox.env.NPM_CONFIG_IGNORE_SCRIPTS, "true");
  assert.equal(readFileSync(sandbox.env.NPM_CONFIG_USERCONFIG, "utf8"), `@sodapop-sh:registry=${publicRegistry}\n`);
  assert.equal(readFileSync(sandbox.env.NPM_CONFIG_GLOBALCONFIG, "utf8"), "");
  assert.equal(JSON.parse(readFileSync(path.join(sandbox.work, "package.json"), "utf8")).private, true);
  for (const key of ["NODE_AUTH_TOKEN", "NPM_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "NODE_PATH", "HTTPS_PROXY"]) {
    assert.equal(sandbox.env[key], undefined, `unexpected inherited ${key}`);
  }
  assert.throws(() => createNpmSandbox(path.join(sandbox.root, "invalid"), { registryInstall: "true" }), /must be a boolean/);
  assert.throws(() => installLocal(sandbox, "cli.tgz", "native.tgz"), /must not seed/);
  assert.throws(() => packPackage(sandbox, "not-read"), /must not pack/);
});

test("injected npm subprocess installs only the exact CLI spec from the public registry", (t) => {
  const sandbox = sandboxFixture(t, true);
  let calls = 0;
  const spawn = (command, args, options) => {
    calls += 1;
    assert.equal(command, process.execPath);
    assert.equal(args[0], sandbox.npm);
    assert.deepEqual(args.slice(1, 7), [
      "install", "--global", "--prefix", sandbox.prefix,
      calls === 1 ? "--include=optional" : "--omit=optional", "@sodapop-sh/cli@1.2.3"
    ]);
    assert.equal(args.at(-2), "--registry");
    assert.equal(args.at(-1), publicRegistry);
    assert.ok(args.includes("--ignore-scripts"));
    assert.ok(args.includes("--offline=false"));
    assert.ok(args.includes("--prefer-online"));
    assert.ok(!args.includes("--offline"));
    assert.ok(!args.some((arg) => arg.endsWith(".tgz") || /^@sodapop-sh\/(darwin|linux|windows)-/.test(arg)));
    assert.equal(options.cwd, sandbox.work);
    assert.equal(options.env, sandbox.env);
    mkdirSync(path.dirname(globalShim(sandbox)), { recursive: true });
    writeFileSync(globalShim(sandbox), "unexecuted shim fixture\n");
    return { status: 0, stdout: "", stderr: "" };
  };
  installRegistry(sandbox, "1.2.3", { spawn });
  installRegistry(sandbox, "1.2.3", { omitOptional: true, spawn });
  assert.equal(calls, 2);
  assert.deepEqual(readdirSync(sandbox.tarballs), []);
});

test("public install fails on unpublished namespace, npm errors, and missing shim without fallback", (t) => {
  const sandbox = sandboxFixture(t, true);
  for (const result of [
    { status: 1, stdout: "", stderr: "E404 @sodapop-sh/cli is not published" },
    { status: null, error: new Error("npm is unavailable") },
    { status: null, signal: "SIGTERM" }
  ]) {
    let calls = 0;
    assert.throws(() => installRegistry(sandbox, "1.2.3", { spawn: () => {
      calls += 1;
      return result;
    } }), /npm install failed/);
    assert.equal(calls, 1);
  }
  assert.throws(() => installRegistry(sandbox, "1.2.3", {
    spawn: () => ({ status: 0, stdout: "", stderr: "" })
  }), /global shim is missing/);
  for (const version of ["latest", "^1.2.3", "v1.2.3", "1.2.3+build", "1.2.3 --registry=other"]) {
    assert.throws(() => installRegistry(sandbox, version, {
      spawn: () => assert.fail("invalid versions must not invoke npm")
    }), /valid semantic version/);
  }
});

test("local mode remains offline and cannot accidentally install from the registry", (t) => {
  const sandbox = sandboxFixture(t);
  assert.throws(() => installRegistry(sandbox, "1.2.3"), /explicit registry mode/);
  runNpm(sandbox, ["pack", "local-package"], { spawn: (command, args, options) => {
    assert.ok(args.includes("--offline"));
    assert.ok(!args.includes("--offline=false"));
    assert.ok(!args.includes("--prefer-online"));
    assert.equal(args.at(-1), "http://127.0.0.1:9");
    assert.equal(options.env.NPM_CONFIG_OFFLINE, "true");
    return { status: 0, stdout: "fixture", stderr: "" };
  } });
});

test("registry hash gate rejects changed bytes, versions, missing optional packages and outside-prefix resolution", (t) => {
  const sandbox = sandboxFixture(t, true);
  const cli = globalPackage(sandbox, "@sodapop-sh/cli");
  const payload = path.join(cli, "node_modules", ...host.package.split("/"));
  mkdirSync(path.join(payload, "bin"), { recursive: true });
  writeJSON(path.join(cli, "package.json"), { name: "@sodapop-sh/cli", version: "1.2.3" });
  writeJSON(path.join(payload, "package.json"), { name: host.package, version: "1.2.3" });
  const binary = path.join(payload, "bin", host.binary);
  const bytes = Buffer.from("unexecuted hash-gate fixture, not a native Sodapop binary");
  writeFileSync(binary, bytes);
  const manifest = {
    version: "1.2.3", artifacts: [{ platform: host.platform, binary_sha256: createHash("sha256").update(bytes).digest("hex") }]
  };
  assert.equal(assertInstalledHash(sandbox, manifest, host), payload);
  writeFileSync(binary, "changed fixture");
  assert.throws(() => assertInstalledHash(sandbox, manifest, host), /does not match/);
  writeFileSync(binary, bytes);
  writeJSON(path.join(payload, "package.json"), { name: host.package, version: "1.2.2" });
  assert.throws(() => assertInstalledHash(sandbox, manifest, host), /1\.2\.2/);
  rmSync(payload, { recursive: true });
  assert.throws(() => assertInstalledHash(sandbox, manifest, host), { code: "SODAPOP_MISSING_PAYLOAD" });

  const external = path.join(sandbox.root, "outside-prefix");
  mkdirSync(path.join(external, "bin"), { recursive: true });
  writeJSON(path.join(external, "package.json"), { name: host.package, version: "1.2.3" });
  writeFileSync(path.join(external, "bin", host.binary), bytes);
  symlinkSync(external, payload, process.platform === "win32" ? "junction" : "dir");
  assert.throws(() => assertInstalledHash(sandbox, manifest, host), /outside the isolated npm prefix/);
});
