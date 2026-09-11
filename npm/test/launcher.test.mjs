import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createRequire } from "node:module";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { spawn, spawnSync } from "node:child_process";
import path from "node:path";
import test, { before, after } from "node:test";
import { buildRunner, host, launcherFixture, TARGETS } from "./helpers.mjs";
import { isolatedEnvironment, temporaryRoot } from "../scripts/install-support.mjs";
import { writeJSON } from "../scripts/build-packages.mjs";

const require = createRequire(import.meta.url);
const { main, platformPackage, requireGlibc, resolvePayload } = require("../packages/cli/lib/launcher.js");
const root = temporaryRoot("launcher-tests");
let runner;
before(() => { runner = buildRunner(root); });
after(() => rmSync(root, { recursive: true, force: true }));
let serial = 0;

function fixture(target = host, binary = runner) {
  const directory = path.join(root, `case-${serial++}`);
  mkdirSync(directory);
  const paths = launcherFixture(directory, binary, target);
  const env = isolatedEnvironment(directory);
  return { ...paths, directory, env, packageRoot: paths.cli, platform: target.os, arch: target.cpu, getReport: () => ({ header: { glibcVersionRuntime: "2.36" } }) };
}

test("maps all supported targets including native Windows architectures and rejects unknown CPUs", () => {
  assert.equal(Object.keys(TARGETS).length, 6);
  for (const target of Object.values(TARGETS)) assert.equal(platformPackage(target.os, target.cpu), target.package);
  assert.equal(platformPackage("win32", "arm64"), "@sodapop-sh/windows-arm64");
  assert.equal(platformPackage("win32", "x64"), "@sodapop-sh/windows-amd64");
  for (const pair of [["win32", "ia32"], ["linux", "riscv64"], ["freebsd", "x64"], ["darwin", "amd64"]]) {
    assert.throws(() => platformPackage(...pair), { code: "SODAPOP_UNSUPPORTED_PLATFORM" });
  }
});

test("Linux fails closed for musl, missing reports, and conflicting libc evidence", () => {
  requireGlibc(() => ({ header: { glibcVersionRuntime: "2.36" } }));
  assert.throws(() => requireGlibc(() => ({ sharedObjects: ["/lib/ld-musl-x86_64.so.1"] })), { code: "SODAPOP_UNSUPPORTED_LIBC" });
  for (const report of [{}, { header: {} }, { header: { glibcVersionRuntime: "2.36" }, sharedObjects: ["/lib/libc.musl-aarch64.so.1"] }]) {
    assert.throws(() => requireGlibc(() => report), { code: "SODAPOP_AMBIGUOUS_LIBC" });
  }
  assert.throws(() => requireGlibc(() => { throw new Error("report unavailable"); }), { code: "SODAPOP_AMBIGUOUS_LIBC" });
});

test("payload resolution distinguishes unavailable, ambiguous, missing, version, and hash failures", () => {
  const f = fixture();
  assert.equal(resolvePayload(f), path.join(f.platformRoot, "bin", host.binary));
  const cliJSON = path.join(f.cli, "package.json");
  writeJSON(cliJSON, { ...f.cliMetadata, optionalDependencies: {} });
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_UNAVAILABLE_PLATFORM" });
  writeJSON(cliJSON, { ...f.cliMetadata, sodapop: { ...f.cliMetadata.sodapop, artifacts: [...f.cliMetadata.sodapop.artifacts, ...f.cliMetadata.sodapop.artifacts] } });
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_AMBIGUOUS_PLATFORM" });
  writeJSON(cliJSON, f.cliMetadata);
  const packageJSON = path.join(f.platformRoot, "package.json");
  writeJSON(packageJSON, { ...f.metadata, version: "9.0.0" });
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_VERSION_MISMATCH" });
  writeJSON(packageJSON, { ...f.metadata, cpu: ["wrong"] });
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_PAYLOAD_MISMATCH" });
  writeJSON(packageJSON, f.metadata);
  writeFileSync(path.join(f.platformRoot, "bin", host.binary), "corrupt");
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_PAYLOAD_MISMATCH" });
  rmSync(path.join(f.platformRoot, "bin", host.binary));
  assert.throws(() => resolvePayload(f), { code: "SODAPOP_MISSING_PAYLOAD" });
  const missing = fixture();
  rmSync(missing.platformRoot, { recursive: true });
  assert.throws(() => resolvePayload(missing), { code: "SODAPOP_MISSING_PAYLOAD" });
});

test("Windows x64 and arm64 resolve their architecture-matched PE fixtures", () => {
  for (const key of ["win32-x64", "win32-arm64"]) {
    const target = TARGETS[key];
    const binary = target === host ? runner : buildRunner(root, target);
    assert.equal(readFileSync(binary).subarray(0, 2).toString(), "MZ");
    const f = fixture(target, binary);
    assert.equal(resolvePayload(f), path.join(f.platformRoot, "bin/sodapop.exe"));
  }
});

test("injected subprocess inherits exact stdio, cwd, arguments, and native status", async () => {
  const f = fixture();
  const runtime = Object.assign(new EventEmitter(), { platform: process.platform, env: f.env, cwd: () => f.directory, pid: 123, kill: () => assert.fail("unexpected signal") });
  const child = Object.assign(new EventEmitter(), { kill: () => assert.fail("unexpected child signal") });
  const args = ["--exit", "37", "value with spaces", "$literal", ""];
  let calls = 0;
  const result = main(args, { ...f, process: runtime, spawn: (binary, actualArgs, options) => {
    calls += 1;
    assert.equal(binary, path.join(f.platformRoot, "bin", host.binary));
    assert.deepEqual(actualArgs, args);
    assert.deepEqual(options, { stdio: "inherit", cwd: f.directory, env: f.env, shell: false });
    queueMicrotask(() => child.emit("exit", 37, null));
    return child;
  } });
  assert.equal(await result, 37);
  assert.equal(calls, 1);
  assert.equal(runtime.listenerCount("SIGINT"), 0);
});

test("subprocess error is stable, not replayed, and removes all signal handlers", async () => {
  const f = fixture();
  const runtime = Object.assign(new EventEmitter(), {
    platform: "linux", env: f.env, cwd: () => f.directory,
    kill: () => assert.fail("must not terminate twice after spawn error")
  });
  const child = new EventEmitter();
  const errors = [];
  const result = main([], { ...f, process: runtime, errorOutput: (error) => errors.push(error), spawn: () => {
    queueMicrotask(() => {
      child.emit("error", new Error("cannot execute fixture"));
      child.emit("exit", null, "SIGTERM");
    });
    return child;
  } });
  assert.equal(await result, 1);
  assert.match(errors[0], /SODAPOP_START_FAILED/);
  assert.equal(errors.length, 1);
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) assert.equal(runtime.listenerCount(signal), 0);
});

test("fallback forwards POSIX signals and preserves termination signal; Windows Ctrl+C is console-owned", async () => {
  for (const platform of ["linux", "win32"]) {
    const f = fixture();
    const forwarded = [];
    const terminated = [];
    const runtime = Object.assign(new EventEmitter(), { platform, pid: 123, env: f.env, cwd: () => f.directory, kill: (...args) => terminated.push(args) });
    const child = Object.assign(new EventEmitter(), { kill: (signal) => forwarded.push(signal) });
    const result = main([], { ...f, process: runtime, spawn: () => child });
    runtime.emit("SIGINT");
    runtime.emit("SIGTERM");
    assert.deepEqual(forwarded, platform === "win32" ? ["SIGTERM"] : ["SIGINT", "SIGTERM"]);
    child.emit("exit", null, "SIGTERM");
    assert.equal(await result, 1);
    assert.deepEqual(terminated, [[123, "SIGTERM"]]);
    assert.equal(runtime.listenerCount("SIGINT"), 0);
  }
});

test("real subprocess fixture receives stdin, stdout/stderr, cwd, spaces, and exit status", () => {
  const f = fixture();
  const args = ["--exit", "23", "value with spaces", ""];
  const result = spawnSync(process.execPath, [path.join(f.cli, "bin/sodapop.js"), ...args], {
    env: f.env, cwd: f.directory, input: "stdin with spaces\n", encoding: "utf8"
  });
  assert.ifError(result.error);
  assert.equal(result.status, 23, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), { fixture: true, args, cwd: f.directory, stdin: "stdin with spaces\n" });
  assert.match(result.stderr, /fixture-stderr/);
});

test("real launcher and Node fallback deliver SIGINT without losing native handler status", { timeout: 20_000 }, async (t) => {
  if (process.platform === "win32") {
    t.skip("POSIX kill is not Windows console Ctrl+C; Windows console behavior is covered by the injected seam and requires a native console runner");
    return;
  }
  for (const fallback of [false, true]) {
    const f = fixture();
    const args = fallback
      ? ["-e", 'require(process.argv[1]).main(["--wait-signal"], {packageRoot:process.argv[2], spawn:require("node:child_process").spawn}).then(code=>process.exitCode=code)', path.join(f.cli, "lib/launcher.js"), f.cli]
      : [path.join(f.cli, "bin/sodapop.js"), "--wait-signal"];
    const child = spawn(process.execPath, args, { cwd: f.directory, env: f.env, stdio: ["ignore", "pipe", "pipe"] });
    t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL"); });
    let stderr = "";
    child.stderr.on("data", (data) => { stderr += data; });
    await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.stdout.once("data", (data) => {
        assert.match(data.toString(), /fixture-ready/);
        child.kill("SIGINT");
      });
      child.once("exit", (code, signal) => {
        try {
          assert.equal(code, 42, stderr);
          assert.equal(signal, null);
          assert.match(stderr, /fixture-interrupted/);
          resolve();
        } catch (error) { reject(error); }
      });
    });
  }
});
