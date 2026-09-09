import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const platformNames = new Set([
  "@sodapop-sh/darwin-amd64",
  "@sodapop-sh/darwin-arm64",
  "@sodapop-sh/linux-amd64",
  "@sodapop-sh/linux-arm64",
  "@sodapop-sh/windows-amd64"
]);

export function validVersion(version) {
  if (typeof version !== "string" || version.length > 64 ||
      !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$/.test(version)) {
    return false;
  }
  const dash = version.indexOf("-");
  return dash === -1 || version.slice(dash + 1).split(".").every(
    (identifier) => !/^\d+$/.test(identifier) || identifier === "0" || !identifier.startsWith("0")
  );
}

function npm(args) {
  return spawnSync("npm", args, { encoding: "utf8", timeout: 300_000 });
}

function readPackages(directory, version) {
  const platformsDirectory = path.join(directory, "platforms");
  const entries = readdirSync(platformsDirectory, { withFileTypes: true });
  if (entries.length === 0 || entries.some((entry) => !entry.isDirectory())) {
    throw new Error("Expected only native package directories under platforms/");
  }
  const directories = entries.map((entry) => path.join(platformsDirectory, entry.name)).sort();
  directories.push(path.join(directory, "cli"));
  const seen = new Set();
  const packages = directories.map((packageDirectory, index) => {
    const metadata = JSON.parse(readFileSync(path.join(packageDirectory, "package.json"), "utf8"));
    const isCLI = index === directories.length - 1;
    if (metadata.version !== version || metadata.private ||
        (isCLI ? metadata.name !== "@sodapop-sh/cli" : !platformNames.has(metadata.name)) ||
        seen.has(metadata.name)) {
      throw new Error(`Invalid generated package metadata in ${packageDirectory}`);
    }
    seen.add(metadata.name);
    return { directory: packageDirectory, metadata };
  });
  const dependencies = packages.at(-1).metadata.optionalDependencies ?? {};
  const native = packages.slice(0, -1).map((entry) => entry.metadata.name).sort();
  if (JSON.stringify(Object.keys(dependencies).sort()) !== JSON.stringify(native) ||
      Object.values(dependencies).some((value) => value !== version)) {
    throw new Error("CLI optional dependencies must exactly match all generated native packages");
  }
  return packages;
}

function resultJSON(result, operation) {
  if (result.error || result.status !== 0) {
    throw new Error(`${operation} failed${result.error ? `: ${result.error.message}` : ` (exit ${result.status})`}`);
  }
  try {
    return JSON.parse(result.stdout);
  } catch {
    throw new Error(`${operation} did not return valid JSON`);
  }
}

function packReceipt(runner, args, metadata, version, operation) {
  const receipts = resultJSON(runner(args), operation);
  if (!Array.isArray(receipts) || receipts.length !== 1) {
    throw new Error(`Expected exactly one packed tarball for ${metadata.name}`);
  }
  const receipt = receipts[0];
  if (receipt.name !== metadata.name || receipt.version !== version ||
      typeof receipt.filename !== "string" ||
      !/^[A-Za-z0-9][A-Za-z0-9.-]*\.tgz$/.test(receipt.filename)) {
    throw new Error(`Unexpected npm pack receipt for ${metadata.name}`);
  }
  return receipt;
}

function receiptFiles(receipt, operation) {
  if (!Array.isArray(receipt.files) || receipt.files.length === 0) {
    throw new Error(`${operation} did not describe its package files`);
  }
  const seen = new Set();
  const files = receipt.files.map((file) => {
    if (!file || typeof file.path !== "string" || file.path === "" ||
        file.path.startsWith("/") || file.path.split("/").includes("..") ||
        !Number.isSafeInteger(file.size) || file.size < 0 ||
        !Number.isSafeInteger(file.mode) || file.mode < 0 || seen.has(file.path)) {
      throw new Error(`${operation} returned invalid package file metadata`);
    }
    seen.add(file.path);
    return { path: file.path, size: file.size, mode: file.mode };
  });
  return files.sort((left, right) => left.path.localeCompare(right.path));
}

function publishedPayloadMatches(runner, staging, packageDirectory, metadata, version, localReceipt, publishedIntegrity) {
  const publishedDirectory = path.join(staging, metadata.name.replace("@", "").replace("/", "-"), "published");
  mkdirSync(publishedDirectory, { recursive: true });
  const specification = `${metadata.name}@${version}`;
  const publishedReceipt = packReceipt(runner, [
    "pack", "--ignore-scripts", "--json", "--pack-destination", publishedDirectory,
    "--registry=https://registry.npmjs.org", specification
  ], metadata, version, `Pack published ${specification}`);
  const publishedTarball = path.join(publishedDirectory, publishedReceipt.filename);
  const downloadedIntegrity = "sha512-" +
    createHash("sha512").update(readFileSync(publishedTarball)).digest("base64");
  if (publishedReceipt.integrity !== publishedIntegrity || downloadedIntegrity !== publishedIntegrity) {
    throw new Error(`Registry metadata and downloaded tarball integrity disagree for ${specification}`);
  }
  if (JSON.stringify(receiptFiles(localReceipt, `Pack ${metadata.name}`)) !==
      JSON.stringify(receiptFiles(publishedReceipt, `Pack published ${specification}`))) {
    return false;
  }
  const difference = runner([
    "diff", "--ignore-scripts", "--color=false",
    `--diff=${specification}`, `--diff=${packageDirectory}`,
    "--registry=https://registry.npmjs.org"
  ]);
  if (difference.error || difference.status !== 0) {
    throw new Error(`Could not compare published payload for ${specification}`);
  }
  return difference.stdout.trim() === "";
}

export function publishPackages({ directory, version, tag }, runner = npm, log = console.log) {
  if (!directory || !validVersion(version) || !["preview", "latest"].includes(tag)) {
    throw new Error("Provide a generated package directory, exact SemVer, and preview or latest tag");
  }
  if (tag === "latest" && version.includes("-")) {
    throw new Error("A prerelease must not be published under latest");
  }
  const packages = readPackages(path.resolve(directory), version);
  const staging = mkdtempSync(path.join(tmpdir(), "sodapop-npm-publish-"));
  try {
    for (const { directory: packageDirectory, metadata } of packages) {
      const localDirectory = path.join(staging, metadata.name.replace("@", "").replace("/", "-"), "local");
      mkdirSync(localDirectory, { recursive: true });
      const receipt = packReceipt(runner, [
        "pack", "--ignore-scripts", "--json", "--pack-destination", localDirectory, packageDirectory
      ], metadata, version, `Pack ${metadata.name}`);
      const tarball = path.join(localDirectory, receipt.filename);
      const integrity = "sha512-" + createHash("sha512").update(readFileSync(tarball)).digest("base64");
      if (integrity !== receipt.integrity) {
        throw new Error(`Packed bytes do not match npm's receipt for ${metadata.name}`);
      }
      const specification = `${metadata.name}@${version}`;
      const existing = runner(["view", specification, "dist.integrity", "--json", "--registry=https://registry.npmjs.org"]);
      if (!existing.error && existing.status === 0) {
        const published = resultJSON(existing, `Read ${specification}`);
        if (published !== integrity) {
          if (!publishedPayloadMatches(
            runner, staging, packageDirectory, metadata, version, receipt, published
          )) {
            throw new Error(`Refusing to replace different published payload for ${specification}`);
          }
          log(`Already published with equivalent payload despite different tar metadata: ${specification}`);
        }
        const tags = resultJSON(runner([
          "view", metadata.name, "dist-tags", "--json", "--registry=https://registry.npmjs.org"
        ]), `Read channel tags for ${metadata.name}`);
        if (!tags || tags[tag] !== version) {
          throw new Error(`${specification} has matching bytes, but ${tag} does not point to it; an owner must explicitly promote the registry tag`);
        }
        log(`Already published with matching integrity: ${specification}`);
        continue;
      }
      let code;
      try {
        code = JSON.parse(existing.stdout).error?.code;
      } catch {
        throw new Error(`Could not determine publication state for ${specification}`);
      }
      if (existing.error || code !== "E404") {
        throw new Error(`Registry lookup failed for ${specification}; refusing to assume it is unpublished`);
      }
      const published = runner([
        "publish", tarball, "--access", "public", "--provenance", "--ignore-scripts",
        "--tag", tag, "--registry=https://registry.npmjs.org"
      ]);
      if (published.error || published.status !== 0) {
        throw new Error(`Publication failed for ${specification}; rerun only with these identical package bytes`);
      }
      log(`Published ${specification} under ${tag}`);
    }
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [directory, version, tag, ...extra] = process.argv.slice(2);
    if (extra.length !== 0) throw new Error("Unexpected arguments");
    publishPackages({ directory, version, tag });
  } catch (error) {
    console.error(`Sodapop npm publication: ${error.message}`);
    process.exitCode = 1;
  }
}
