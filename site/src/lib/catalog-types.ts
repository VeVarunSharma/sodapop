/**
 * Serializable website data, safe to import with `import type` in Astro/React.
 * Astro pages use src/lib/catalog.ts. Configuration/preparation validate the
 * snapshot with scripts/release-catalog.mjs before route bundling; that reader
 * uses source-relative Node paths and must not run from a bundled route.
 * "Published" describes available public metadata with matching release bindings,
 * not publisher authentication, signing, attestation verification, installation
 * qualification, or an independent download of the native executable.
 */
export type NativePlatform =
  | 'darwin/arm64'
  | 'darwin/amd64'
  | 'linux/arm64'
  | 'linux/amd64'
  | 'windows/amd64';

export interface NativeArtifact {
  platform: NativePlatform;
  label: string;
  /** Exact basename from the published manifest, never a latest/download guess. */
  archive: string;
  /** Unix tar.gz or Windows portable ZIP; Windows contains sodapop.exe. */
  format: 'tar.gz' | 'zip';
  binary: 'sodapop' | 'sodapop.exe';
  archiveUrl: string;
  checksumUrl: string;
  archiveSha256: string;
  binarySha256: string;
}

export interface UnpublishedChannel {
  status: 'unpublished';
  command: null;
  version: null;
  url: null;
  platforms: [];
}

export interface PublishedChannel {
  status: 'published';
  command: string;
  version: string;
  url: string;
  /**
   * Exact, nonempty advertised set in canonical native-platform order. Render
   * this list rather than inferring support from templates or native downloads.
   * Every declared npm package must be present; missing packages never silently
   * shrink the set. Windows requires its declared, matching ZIP/package binding.
   */
  platforms: NativePlatform[];
}

export type InstallChannel = UnpublishedChannel | PublishedChannel;

export interface PreReleaseCatalog {
  schemaVersion: 1;
  mode: 'pre-release';
  /** Explicit offline override from SODAPOP_SITE_PREVIEW=1; never persisted. */
  preview?: true;
  version: null;
  releaseUrl: null;
  manifestUrl: null;
  commit: null;
  copilotSdkVersion: null;
  copilotRuntimeVersion: null;
  artifacts: [];
  channels: { npm: UnpublishedChannel; homebrew: UnpublishedChannel };
}

export interface PublishedReleaseCatalog {
  schemaVersion: 1;
  mode: 'release';
  preview?: never;
  version: string;
  releaseUrl: string;
  manifestUrl: string;
  commit: string;
  copilotSdkVersion: string;
  copilotRuntimeVersion: string;
  /** All five advertised public targets; local-check subsets cannot replace them. */
  artifacts: NativeArtifact[];
  /** npm follows its published manifest's declared set; Homebrew remains Unix-only. */
  channels: { npm: InstallChannel; homebrew: InstallChannel };
}

export type ReleaseCatalog = PreReleaseCatalog | PublishedReleaseCatalog;
export type Catalog = ReleaseCatalog;

/**
 * Operator-owned src/data/channels.json. No environment variables can promote a
 * release, change an endpoint, supply credentials, or enable fixture metadata.
 * null releaseTag means GitHub's public latest stable release; otherwise use an
 * exact vX.Y.Z tag. Confirm npm namespace/tap ownership before enabling verify.
 * A channel's verify flag enables a lookup, not publication.
 * npm availability comes from the registry manifest and its matching packages,
 * not the development launcher's optionalDependencies. Local subset fixtures do
 * not establish registry availability or cross-platform installation readiness.
 * SODAPOP_SITE_PREVIEW=1 only suppresses release metadata for offline PR previews;
 * it returns a pre-release catalog with preview:true, without changing this file.
 */
export interface ReleaseConfiguration {
  schemaVersion: 1;
  mode: 'pre-release' | 'release';
  repository: 'VeVarunSharma/sodapop';
  releaseTag: string | null;
  channels: {
    npm: { verify: boolean; package: '@sodapop-sh/cli' };
    homebrew: {
      verify: boolean;
      repository: 'VeVarunSharma/homebrew-sodapop' | null;
      formula: 'sodapop';
    };
  };
}
