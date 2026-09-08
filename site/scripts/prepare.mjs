import { prepareContent } from './prepare-content.mjs';
import { prepareAssets } from './prepare-assets.mjs';
import { readCatalog } from './release-catalog.mjs';
import { installationReference } from '../src/lib/install-methods.mjs';

const catalog = await readCatalog();
await prepareAssets();
await prepareContent({ installationReference: installationReference(catalog) });
