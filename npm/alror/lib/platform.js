'use strict';
// Platform mapping shared by the launcher, the postinstall fallback and the
// release scripts. Node's (process.platform, process.arch) map to one npm
// package `@alror/cli-<platform>-<arch>` and one GoReleaser archive
// `alror_<version>_<goos>_<goarch>.<ext>`.

const REPO = 'manaskumar3003/alror-cli';
const SCOPE = '@alror';

/** @type {Record<string, {goos: string, goarch: string, ext: string}>} */
const PLATFORMS = {
  'linux-x64': { goos: 'linux', goarch: 'amd64', ext: 'tar.gz' },
  'linux-arm64': { goos: 'linux', goarch: 'arm64', ext: 'tar.gz' },
  'darwin-x64': { goos: 'darwin', goarch: 'amd64', ext: 'tar.gz' },
  'darwin-arm64': { goos: 'darwin', goarch: 'arm64', ext: 'tar.gz' },
  'win32-x64': { goos: 'windows', goarch: 'amd64', ext: 'zip' },
  'win32-arm64': { goos: 'windows', goarch: 'arm64', ext: 'zip' },
};

function platformKey(platform = process.platform, arch = process.arch) {
  return `${platform}-${arch}`;
}

function packageName(key) {
  return `${SCOPE}/cli-${key}`;
}

function exeName(name, platform = process.platform) {
  return platform === 'win32' ? `${name}.exe` : name;
}

function archiveName(version, key) {
  const p = PLATFORMS[key];
  if (!p) throw new Error(`unsupported platform ${key}`);
  return `alror_${stripV(version)}_${p.goos}_${p.goarch}.${p.ext}`;
}

function stripV(version) {
  return String(version).replace(/^v/, '');
}

module.exports = { REPO, SCOPE, PLATFORMS, platformKey, packageName, exeName, archiveName, stripV };
