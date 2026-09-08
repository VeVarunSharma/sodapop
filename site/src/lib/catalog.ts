import type { ReleaseCatalog } from './catalog-types';

// Configuration validates this public snapshot before Vite bundles any routes.
// Rendered pages must not resolve filesystem paths from bundled import.meta.url.
export function getCatalog(): ReleaseCatalog {
  return __SODAPOP_RELEASE_CATALOG__;
}
