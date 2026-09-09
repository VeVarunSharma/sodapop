import { unified } from 'unified';
import remarkStringify from 'remark-stringify';

/** @typedef {{id: string, label: string, command: string, description: string}} InstallMethod */
/** @typedef {{status: 'unpublished'|'published', command: string|null, platforms?: string[]}} InstallChannel */

const platformLabels = {
  'darwin/arm64': 'macOS Apple silicon',
  'darwin/amd64': 'macOS Intel',
  'linux/arm64': 'Linux ARM64 (glibc)',
  'linux/amd64': 'Linux x64 (glibc)',
  'windows/amd64': 'Windows x64',
};

/**
 * @param {{channels: {npm: InstallChannel, homebrew: InstallChannel}}} catalog
 * @returns {[InstallMethod, ...InstallMethod[]]}
 */
export function installMethods(catalog) {
  /** @type {[InstallMethod, ...InstallMethod[]]} */
  const methods = [
    {
      id: 'source',
      label: 'Source: macOS / Linux',
      command: 'make build',
      description: "From a source checkout. You'll need Go, Git, network access, and Sodapop's public OAuth client configuration to sign in.",
    },
    {
      id: 'source-windows',
      label: 'Source: Windows',
      command: 'bash scripts/build.sh',
      description: 'Windows x64 source build from Git Bash. Read the build guide for the Go toolchain, public-client configuration, and executable path.',
    },
  ];
  for (const id of /** @type {const} */ (['npm', 'homebrew'])) {
    const channel = catalog.channels[id];
    if (!channel || !['published', 'unpublished'].includes(channel.status)) {
      throw new Error(`Installation catalog is missing a valid ${id} channel`);
    }
    if (channel.status === 'published') {
      if (!channel.command) throw new Error(`Published ${id} channel has no installation command`);
      if (!channel.platforms?.length || channel.platforms.some((platform) => !Object.hasOwn(platformLabels, platform))) {
        throw new Error(`Published ${id} channel has no valid advertised platform set`);
      }
      const supported = channel.platforms.map((platform) => platformLabels[platform]).join(', ');
      methods.unshift({
        id,
        label: id === 'homebrew' ? 'Homebrew' : 'npm',
        command: channel.command,
        description: id === 'homebrew'
          ? `The owned Homebrew tap. This version supports ${supported}.`
          : `Requires Node.js 18 or newer. This published package supports ${supported}.`,
      });
    }
  }
  return methods;
}

/** @param {{mode: string, channels: {npm: InstallChannel, homebrew: InstallChannel}}} catalog */
export function installationReference(catalog) {
  const methods = installMethods(catalog);
  /** @type {import('mdast').Root} */
  const tree = {
    type: 'root',
    children: [
      { type: 'heading', depth: 2, children: [{ type: 'text', value: 'Installation choices' }] },
      { type: 'paragraph', children: [{ type: 'text', value: catalog.mode === 'pre-release'
        ? 'This site is in pre-release mode. Public binary and package-manager downloads are not enabled; the source-build options below require a checkout and the documented prerequisites.'
        : 'These commands use the same validated channel catalog as the download page. Choose only a method that supports your platform.' }] },
    ],
  };
  for (const method of methods) {
    tree.children.push(
      { type: 'heading', depth: 3, children: [{ type: 'text', value: method.label }] },
      { type: 'paragraph', children: [{ type: 'text', value: method.description }] },
      { type: 'code', lang: 'sh', value: method.command },
    );
    if (method.id === 'npm') {
      tree.children.push(
        { type: 'paragraph', children: [{ type: 'text', value: 'Verify the installed command and bundled runtime before signing in:' }] },
        { type: 'code', lang: 'sh', value: 'sodapop --version\nsodapop --check-runtime' },
        { type: 'paragraph', children: [{ type: 'text', value: 'Update to the newest stable release or remove the npm-owned command:' }] },
        { type: 'code', lang: 'sh', value: 'npm install --global @sodapop-sh/cli@latest\nnpm uninstall --global @sodapop-sh/cli' },
        { type: 'paragraph', children: [{ type: 'text', value: 'Installing @latest replaces an older @preview installation. If sodapop is not found after a successful install, run npm prefix --global and ensure npm’s global executable directory is on your user PATH. Use a user-owned Node.js/npm installation instead of sudo.' }] },
      );
    }
  }
  return unified().use(remarkStringify, { fences: true }).stringify(tree);
}
