import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import {
  commandFailureDetail,
  expectedExecutableModeDifference,
  publishPackages,
  validVersion
} from "./publish-npm.mjs";

function fixture(t) {
  const root = mkdtempSync(path.join(tmpdir(), "sodapop-publish-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const native = [
    "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64",
    "windows-amd64", "windows-arm64"
  ];
  const dependencies = Object.fromEntries(native.map((id) => [`@sodapop-sh/${id}`, "1.2.3"]));
  const entries = native.map((id) => [
    `platforms/${id}`, { name: `@sodapop-sh/${id}`, version: "1.2.3" }
  ]);
  entries.push(["cli", {
    name: "@sodapop-sh/cli", version: "1.2.3", optionalDependencies: dependencies
  }]);
  for (const [directory, metadata] of entries) {
    mkdirSync(path.join(root, directory), { recursive: true });
    writeFileSync(path.join(root, directory, "package.json"), JSON.stringify(metadata));
  }
  return root;
}

function registry(mode = "missing") {
  const published = [];
  const compared = [];
  const data = Buffer.from("unit test tarball receipt");
  const integrity = "sha512-" + createHash("sha512").update(data).digest("base64");
  const runner = (args, options = {}) => {
    if (args[0] === "pack") {
      const metadata = JSON.parse(readFileSync(path.join(args.at(-1), "package.json")));
      const filename = metadata.name.replace("@", "").replace("/", "-") + "-1.2.3.tgz";
      const destination = args[args.indexOf("--pack-destination") + 1];
      writeFileSync(path.join(destination, filename), data);
      return { status: 0, stdout: JSON.stringify([{ ...metadata, filename, integrity }]) };
    }
    if (args[0] === "view") {
      if (args[2] === "dist-tags") {
        return { status: 0, stdout: JSON.stringify({ latest: mode === "wrong-tag" ? "1.2.2" : "1.2.3", preview: "1.2.3" }) };
      }
      if (mode === "matching" || mode === "mode-only" || mode === "different" ||
          mode === "wrong-tag" || mode === "comparison-error") {
        return { status: 0, stdout: JSON.stringify(mode === "different" ? "sha512-other" : integrity) };
      }
      return {
        status: 1,
        stdout: JSON.stringify({ error: { code: ["missing", "publish-error"].includes(mode) ? "E404" : "ENOTCONN" } })
      };
    }
    if (args[0] === "diff") {
      compared.push(args);
      assert.ok(options.cwd.endsWith("cli") ||
        options.cwd.includes(`${path.sep}platforms${path.sep}`));
      assert.equal(args.filter((arg) => arg.startsWith("--diff=")).length, 1);
      assert.ok(!args.includes("--diff-name-only"));
      if (mode === "comparison-error") return { status: 1, stdout: "", stderr: "registry unavailable" };
      if (mode === "mode-only") {
        return {
          status: 0,
          stdout: [
            "diff --git a/bin/sodapop b/bin/sodapop",
            "old mode 100644",
            "new mode 100755",
            "index v1.2.3..v1.2.3 ",
            "--- a/bin/sodapop",
            "+++ b/bin/sodapop",
            ""
          ].join("\n")
        };
      }
      return { status: 0, stdout: mode === "different" ? "package.json\n" : "" };
    }
    assert.equal(args[0], "publish");
    assert.ok(args.includes("--provenance"));
    assert.ok(args.includes("--ignore-scripts"));
    published.push(path.basename(args[1]));
    if (mode === "publish-error") {
      return {
        status: 1,
        stdout: "",
        stderr: "npm error E403 Trusted publisher rejected npm_secretTokenValue1234567890"
      };
    }
    return { status: 0, stdout: "" };
  };
  return { runner, published, compared };
}

test("publishes platform tarballs before the exact-version launcher", (t) => {
  const directory = fixture(t);
  const { runner, published } = registry();
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, runner, () => {});
  assert.deepEqual(published, [
    "sodapop-sh-darwin-amd64-1.2.3.tgz",
    "sodapop-sh-darwin-arm64-1.2.3.tgz",
    "sodapop-sh-linux-amd64-1.2.3.tgz",
    "sodapop-sh-linux-arm64-1.2.3.tgz",
    "sodapop-sh-windows-amd64-1.2.3.tgz",
    "sodapop-sh-windows-arm64-1.2.3.tgz",
    "sodapop-sh-cli-1.2.3.tgz"
  ]);
});

test("retry accepts only identical already-published package contents", (t) => {
  const directory = fixture(t);
  const matching = registry("matching");
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, matching.runner, () => {});
  assert.deepEqual(matching.published, []);
  assert.equal(matching.compared.length, 7);
  const modeOnly = registry("mode-only");
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, modeOnly.runner, () => {});
  assert.deepEqual(modeOnly.published, []);
  const different = registry("different");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, different.runner, () => {}),
    /different published package contents/
  );
  assert.deepEqual(different.published, []);
  const wrongTag = registry("wrong-tag");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, wrongTag.runner, () => {}),
    /explicitly promote/
  );
  assert.deepEqual(wrongTag.published, []);
});

test("mode-only retries accept only the expected executable normalization", () => {
  const valid = [
    "diff --git a/bin/sodapop b/bin/sodapop",
    "old mode 100644",
    "new mode 100755",
    "index v1.2.3..v1.2.3",
    "--- a/bin/sodapop",
    "+++ b/bin/sodapop"
  ].join("\n");
  assert.equal(expectedExecutableModeDifference(valid), true);
  assert.equal(expectedExecutableModeDifference(
    valid.replace("old mode 100644\nnew mode 100755", "old mode 100755\nnew mode 100644")
  ), true);
  assert.equal(expectedExecutableModeDifference(valid.replaceAll("sodapop", "sodapop.exe")), true);
  assert.equal(expectedExecutableModeDifference(valid.replaceAll("sodapop", "sodapop.js")), true);
  for (const invalid of [
    valid.replace("new mode 100755", "new mode 100644"),
    valid.replace("new mode 100755", "new mode 100700"),
    valid.replaceAll("bin/sodapop", "package.json"),
    `${valid}\n@@ -1 +1 @@\n-old\n+new`
  ]) {
    assert.equal(expectedExecutableModeDifference(invalid), false);
  }
});

test("published-package comparison failures stop without publishing", (t) => {
  const directory = fixture(t);
  const failed = registry("comparison-error");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, failed.runner, () => {}),
    /Could not compare published contents/
  );
  assert.deepEqual(failed.published, []);
});

test("publication failures retain actionable registry diagnostics without tokens", (t) => {
  const directory = fixture(t);
  const failed = registry("publish-error");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, failed.runner, () => {}),
    (error) => {
      assert.match(error.message, /E403 Trusted publisher rejected/);
      assert.match(error.message, /\[redacted npm token\]/);
      assert.doesNotMatch(error.message, /npm_secretTokenValue/);
      return true;
    }
  );
  assert.equal(
    commandFailureDetail({ status: 1, stderr: "//registry.npmjs.org/:_authToken=secret" }),
    "//registry.npmjs.org/:_authToken=[redacted]"
  );
});

test("network failure is not mistaken for an unpublished package", (t) => {
  const directory = fixture(t);
  const { runner, published } = registry("offline");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "preview" }, runner, () => {}),
    /refusing to assume/
  );
  assert.deepEqual(published, []);
});

test("rejects mismatched dependencies and prereleases under latest", (t) => {
  const directory = fixture(t);
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3-rc.1", tag: "latest" }),
    /prerelease/
  );
  const filename = path.join(directory, "cli", "package.json");
  const metadata = JSON.parse(readFileSync(filename));
  metadata.optionalDependencies["@sodapop-sh/darwin-arm64"] = "^1.2.3";
  writeFileSync(filename, JSON.stringify(metadata));
  assert.throws(() => publishPackages({ directory, version: "1.2.3", tag: "latest" }), /exactly match/);
});

test("rejects generated native package names outside the six-platform allowlist", (t) => {
  const directory = fixture(t);
  const metadata = { name: "@sodapop-sh/windows-ia32", version: "1.2.3" };
  const packageDirectory = path.join(directory, "platforms", "windows-ia32");
  mkdirSync(packageDirectory);
  writeFileSync(path.join(packageDirectory, "package.json"), JSON.stringify(metadata));
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }),
    /Invalid generated package metadata/
  );
});

test("requires exact npm-compatible release versions", () => {
  for (const version of ["1.2.3", "1.2.3-rc.1", "0.0.0-test"]) assert.ok(validVersion(version));
  for (const version of ["v1.2.3", "01.2.3", "1.2.3-01", "1.2.3+build", "latest", "../1.2.3"]) {
    assert.equal(validVersion(version), false, version);
  }
});
