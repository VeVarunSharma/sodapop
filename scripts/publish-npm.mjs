import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, readdirSync, rmSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const platformNames = new Set([
  "@sodapop-sh/darwin-amd64",
  "@sodapop-sh/darwin-arm64",
  "@sodapop-sh/linux-amd64",
  "@sodapop-sh/linux-arm64",
  "@sodapop-sh/windows-amd64",
  "@sodapop-sh/windows-arm64"
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

function npm(args, options = {}) {
  return spawnSync("npm", args, {
    cwd: options.cwd,
    encoding: "utf8",
    timeout: 300_000
  });
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

export function commandFailureDetail(result) {
  if (result.error) return result.error.message;
  const output = [result.stderr, result.stdout]
    .filter((value) => typeof value === "string" && value.trim() !== "")
    .join("\n")
    .replace(/\u001b\[[0-9;]*m/g, "")
    .replace(/\bnpm_[A-Za-z0-9_-]{20,}\b/g, "[redacted npm token]")
    .replace(/(\/\/registry\.npmjs\.org\/:_authToken=)\S+/gi, "$1[redacted]")
    .replace(/\bBearer\s+\S+/gi, "Bearer [redacted]")
    .trim();
  return output ? output.slice(-4096) : `exit ${result.status}`;
}

export function expectedExecutableModeDifference(output) {
  const lines = output.trim().split(/\r?\n/).map((line) => line.trimEnd());
  if (lines.length !== 6) return false;
  const match = lines[0].match(/^diff --git a\/(bin\/sodapop(?:\.exe|\.js)?) b\/\1$/);
  const oldMode = lines[1].match(/^old mode (100644|100755)$/)?.[1];
  const newMode = lines[2].match(/^new mode (100644|100755)$/)?.[1];
  if (!match || !oldMode || !newMode || oldMode === newMode ||
      !/^index \S+\.\.\S+(?: \d+)?$/.test(lines[3]) ||
      lines[4] !== `--- a/${match[1]}` || lines[5] !== `+++ b/${match[1]}`) {
    return false;
  }
  return true;
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
      const specification = `${metadata.name}@${version}`;
      const existing = runner(["view", specification, "dist.integrity", "--json", "--registry=https://registry.npmjs.org"]);
      if (!existing.error && existing.status === 0) {
        const publishedIntegrity = resultJSON(existing, `Read ${specification}`);
        if (typeof publishedIntegrity !== "string" || !publishedIntegrity.startsWith("sha512-")) {
          throw new Error(`${specification} does not expose a valid registry integrity`);
        }
        const comparison = runner([
          "diff", `--diff=${specification}`,
          "--registry=https://registry.npmjs.org"
        ], { cwd: packageDirectory });
        if (comparison.error || comparison.status !== 0) {
          throw new Error(`Could not compare published contents for ${specification}`);
        }
        const difference = comparison.stdout.trim();
        if (difference !== "" && !expectedExecutableModeDifference(difference)) {
          const detail = difference.slice(0, 4096);
          throw new Error(`Refusing to replace different published package contents for ${specification}: ${detail}`);
        }
        if (difference !== "") {
          log(`Published payload differs only by npm's executable mode normalization: ${specification}`);
        }
        const tags = resultJSON(runner([
          "view", metadata.name, "dist-tags", "--json", "--registry=https://registry.npmjs.org"
        ]), `Read channel tags for ${metadata.name}`);
        if (!tags || tags[tag] !== version) {
          throw new Error(`${specification} has matching contents, but ${tag} does not point to it; an owner must explicitly promote the registry tag`);
        }
        log(`Already published with matching contents: ${specification}`);
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

      const receipts = resultJSON(runner([
        "pack", "--ignore-scripts", "--json", "--pack-destination", staging, packageDirectory
      ]), `Pack ${metadata.name}`);
      if (!Array.isArray(receipts) || receipts.length !== 1) {
        throw new Error(`Expected exactly one packed tarball for ${metadata.name}`);
      }
      const receipt = receipts[0];
      if (receipt.name !== metadata.name || receipt.version !== version ||
          typeof receipt.filename !== "string" ||
          !/^[A-Za-z0-9][A-Za-z0-9.-]*\.tgz$/.test(receipt.filename)) {
        throw new Error(`Unexpected npm pack receipt for ${metadata.name}`);
      }
      const tarball = path.join(staging, receipt.filename);
      const integrity = "sha512-" + createHash("sha512").update(readFileSync(tarball)).digest("base64");
      if (integrity !== receipt.integrity) {
        throw new Error(`Packed bytes do not match npm's receipt for ${metadata.name}`);
      }
      const published = runner([
        "publish", tarball, "--access", "public", "--provenance", "--ignore-scripts",
        "--tag", tag, "--registry=https://registry.npmjs.org"
      ]);
      if (published.error || published.status !== 0) {
        throw new Error(
          `Publication failed for ${specification}; rerun only with these identical package bytes: ` +
          commandFailureDetail(published)
        );
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
