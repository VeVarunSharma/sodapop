import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { publishPackages, validVersion } from "./publish-npm.mjs";

function fixture(t) {
  const root = mkdtempSync(path.join(tmpdir(), "sodapop-publish-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const entries = [
    ["platforms/darwin-arm64", { name: "@sodapop-sh/darwin-arm64", version: "1.2.3" }],
    ["cli", {
      name: "@sodapop-sh/cli", version: "1.2.3",
      optionalDependencies: { "@sodapop-sh/darwin-arm64": "1.2.3" }
    }]
  ];
  for (const [directory, metadata] of entries) {
    mkdirSync(path.join(root, directory), { recursive: true });
    writeFileSync(path.join(root, directory, "package.json"), JSON.stringify(metadata));
  }
  return root;
}

function registry(mode = "missing") {
  const published = [];
  const data = Buffer.from("unit test tarball receipt");
  const integrity = "sha512-" + createHash("sha512").update(data).digest("base64");
  const publishedData = mode === "matching" || mode === "wrong-tag"
    ? data
    : Buffer.from("published tar container");
  const publishedIntegrity = "sha512-" + createHash("sha512").update(publishedData).digest("base64");
  const filesByName = new Map();
  const runner = (args) => {
    if (args[0] === "pack") {
      const specification = args.at(-1);
      const fromRegistry = specification.startsWith("@sodapop-sh/") && specification.includes("@1.2.3");
      const metadata = fromRegistry
        ? { name: specification.slice(0, specification.lastIndexOf("@")), version: "1.2.3" }
        : JSON.parse(readFileSync(path.join(specification, "package.json")));
      const filename = metadata.name.replace("@", "").replace("/", "-") + "-1.2.3.tgz";
      const destination = args[args.indexOf("--pack-destination") + 1];
      const bytes = fromRegistry ? publishedData : data;
      writeFileSync(path.join(destination, filename), bytes);
      const localFiles = [{
        path: "package.json",
        size: fromRegistry
          ? filesByName.get(metadata.name)[0].size
          : readFileSync(path.join(specification, "package.json")).length,
        mode: 0o644
      }];
      if (!fromRegistry) filesByName.set(metadata.name, localFiles);
      const files = localFiles.map((file) => ({
        ...file,
        mode: fromRegistry && mode === "different-mode" ? 0o755 : file.mode
      }));
      return {
        status: 0,
        stdout: JSON.stringify([{
          ...metadata, filename, files,
          integrity: fromRegistry ? publishedIntegrity : integrity
        }])
      };
    }
    if (args[0] === "view") {
      if (args[2] === "dist-tags") {
        return { status: 0, stdout: JSON.stringify({ latest: mode === "wrong-tag" ? "1.2.2" : "1.2.3", preview: "1.2.3" }) };
      }
      if (["matching", "equivalent", "different", "different-mode", "wrong-tag"].includes(mode)) {
        return { status: 0, stdout: JSON.stringify(publishedIntegrity) };
      }
      return {
        status: 1,
        stdout: JSON.stringify({ error: { code: mode === "missing" ? "E404" : "ENOTCONN" } })
      };
    }
    if (args[0] === "diff") {
      assert.ok(args.includes("--registry=https://registry.npmjs.org"));
      return {
        status: 0,
        stdout: mode === "different" ? "diff --git a/package.json b/package.json\n" : ""
      };
    }
    assert.equal(args[0], "publish");
    assert.ok(args.includes("--provenance"));
    assert.ok(args.includes("--ignore-scripts"));
    published.push(path.basename(args[1]));
    return { status: 0, stdout: "" };
  };
  return { runner, published };
}

test("publishes platform tarballs before the exact-version launcher", (t) => {
  const directory = fixture(t);
  const { runner, published } = registry();
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, runner, () => {});
  assert.deepEqual(published, ["sodapop-sh-darwin-arm64-1.2.3.tgz", "sodapop-sh-cli-1.2.3.tgz"]);
});

test("retry accepts identical tarballs or equivalent payloads only", (t) => {
  const directory = fixture(t);
  const matching = registry("matching");
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, matching.runner, () => {});
  assert.deepEqual(matching.published, []);
  const equivalent = registry("equivalent");
  publishPackages({ directory, version: "1.2.3", tag: "latest" }, equivalent.runner, () => {});
  assert.deepEqual(equivalent.published, []);
  const different = registry("different");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, different.runner, () => {}),
    /different published payload/
  );
  assert.deepEqual(different.published, []);
  const differentMode = registry("different-mode");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, differentMode.runner, () => {}),
    /different published payload/
  );
  assert.deepEqual(differentMode.published, []);
  const wrongTag = registry("wrong-tag");
  assert.throws(
    () => publishPackages({ directory, version: "1.2.3", tag: "latest" }, wrongTag.runner, () => {}),
    /explicitly promote/
  );
  assert.deepEqual(wrongTag.published, []);
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

test("requires exact npm-compatible release versions", () => {
  for (const version of ["1.2.3", "1.2.3-rc.1", "0.0.0-test"]) assert.ok(validVersion(version));
  for (const version of ["v1.2.3", "01.2.3", "1.2.3-01", "1.2.3+build", "latest", "../1.2.3"]) {
    assert.equal(validVersion(version), false, version);
  }
});
