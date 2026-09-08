// Preload only in release-test subprocesses, even if the committed mode changes.
globalThis.fetch = async () => {
  process.exitCode = 1;
  throw new Error('Release test subprocesses cannot make live network requests');
};
