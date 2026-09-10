import test from 'node:test';
import assert from 'node:assert/strict';
import { installMethods, installationReference } from '../../src/lib/install-methods.mjs';

const channels = () => ({
  npm: { status: 'unpublished', command: null },
  homebrew: { status: 'unpublished', command: null },
});

test('pre-release has real platform-specific source commands, never invented package installs', () => {
  const catalog = { mode: 'pre-release', channels: channels() };
  assert.deepEqual(installMethods(catalog).map(({ command }) => command), ['make build', 'bash scripts/build.sh']);
  const reference = installationReference(catalog);
  assert.match(reference, /pre-release mode/);
  assert.match(reference, /Windows/);
  assert.doesNotMatch(reference, /npm install|brew install/);
});

test('UI and documentation share verified channel commands', () => {
  const catalog = { mode: 'release', channels: channels() };
  catalog.channels.npm = { status: 'published', command: 'npm install --global @sodapop-sh/cli@latest', platforms: ['darwin/arm64', 'windows/amd64'] };
  const methods = installMethods(catalog);
  assert.equal(methods[0].id, 'npm');
  assert.ok(installationReference(catalog).includes(methods[0].command));
  assert.equal(methods.some(({ id }) => id === 'homebrew'), false);
  assert.match(methods[0].description, /Windows x64/);
  assert.doesNotMatch(methods[0].description, /Linux/);
  catalog.channels.npm.platforms = [];
  assert.throws(() => installMethods(catalog), /platform set/);
  catalog.channels.npm.command = null;
  assert.throws(() => installMethods(catalog), /no installation command/);
});

test('npm remains the first install option when Homebrew is also published', () => {
  const catalog = { mode: 'release', channels: channels() };
  catalog.channels.npm = {
    status: 'published',
    command: 'npm install --global @sodapop-sh/cli@latest',
    platforms: ['darwin/arm64', 'linux/amd64', 'windows/amd64'],
  };
  catalog.channels.homebrew = {
    status: 'published',
    command: 'brew install VeVarunSharma/sodapop/sodapop',
    platforms: ['darwin/arm64', 'linux/amd64'],
  };
  assert.deepEqual(installMethods(catalog).map(({ id }) => id),
    ['npm', 'homebrew', 'source', 'source-windows']);
});
