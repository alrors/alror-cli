'use strict';
// Fallback installer: used only when the optional platform package
// (@alror/cli-<os>-<cpu>) is missing, e.g. after `npm i --no-optional` or
// `--omit=optional`. Downloads the GitHub release archive, verifies its
// SHA-256 against checksums.txt and extracts the binaries into vendor/.

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const { REPO, PLATFORMS, platformKey, packageName, exeName, archiveName, stripV } = require('./platform');
const { readArchive, findEntry } = require('./archive');

const PKG_DIR = path.join(__dirname, '..');
const BINARIES = ['alror', 'alror-docs'];

function truthy(v) {
  return v !== undefined && v !== '' && !/^(0|false|no|off)$/i.test(String(v));
}

function vendorDir(pkgDir = PKG_DIR) {
  return path.join(pkgDir, 'vendor');
}

/** Parse GoReleaser's checksums.txt ("<sha256>  <file>" per line). */
function parseChecksums(text) {
  const out = new Map();
  for (const line of String(text).split(/\r?\n/)) {
    const m = line.trim().match(/^([a-f0-9]{64})\s+\*?(.+)$/i);
    if (m) out.set(m[2].trim(), m[1].toLowerCase());
  }
  return out;
}

function sha256(buf) {
  return crypto.createHash('sha256').update(buf).digest('hex');
}

function platformPackageInstalled(key, requireResolve = require.resolve) {
  try {
    requireResolve(`${packageName(key)}/package.json`, { paths: [PKG_DIR] });
    return true;
  } catch {
    return false;
  }
}

/**
 * Ensure a binary is available. Never throws for "nothing to do" cases.
 * @param {object} [o]
 * @param {string} [o.version]  package version (defaults to package.json)
 * @param {string} [o.platform]
 * @param {string} [o.arch]
 * @param {NodeJS.ProcessEnv} [o.env]
 * @param {string} [o.pkgDir]
 * @param {(url: string) => Promise<Buffer>} [o.download]
 * @param {(id: string, opts?: object) => string} [o.requireResolve]
 * @param {(msg: string) => void} [o.log]
 * @returns {Promise<{status: 'skipped'|'unsupported'|'optional-present'|'cached'|'downloaded', dir?: string}>}
 */
async function ensureBinary(o = {}) {
  const env = o.env || process.env;
  const pkgDir = o.pkgDir || PKG_DIR;
  const platform = o.platform || process.platform;
  const arch = o.arch || process.arch;
  const log = o.log || (() => {});
  const version = stripV(o.version || require(path.join(pkgDir, 'package.json')).version);

  if (truthy(env.ALROR_SKIP_DOWNLOAD)) return { status: 'skipped' };
  const key = platformKey(platform, arch);
  if (!PLATFORMS[key]) return { status: 'unsupported' };
  if (!o.force && platformPackageInstalled(key, o.requireResolve)) return { status: 'optional-present' };

  const dir = vendorDir(pkgDir);
  const main = path.join(dir, exeName('alror', platform));
  const stamp = path.join(dir, '.version');
  if (fs.existsSync(main) && fs.existsSync(stamp) && fs.readFileSync(stamp, 'utf8').trim() === version) {
    return { status: 'cached', dir };
  }

  const dl = o.download || ((url) => require('./download').download(url, { env }));
  const base = (env.ALROR_DOWNLOAD_BASE || `https://github.com/${REPO}/releases/download/v${version}`).replace(/\/+$/, '');
  const archive = archiveName(version, key);
  log(`alror: ${packageName(key)} is not installed; downloading ${archive} from ${base}`);

  const [archiveBuf, sumsBuf] = await Promise.all([dl(`${base}/${archive}`), dl(`${base}/checksums.txt`)]);
  const expected = parseChecksums(sumsBuf.toString('utf8')).get(archive);
  if (!expected) throw new Error(`checksums.txt has no entry for ${archive}`);
  const actual = sha256(archiveBuf);
  if (actual !== expected) throw new Error(`checksum mismatch for ${archive}: expected ${expected}, got ${actual}`);

  const files = readArchive(archive, archiveBuf);
  fs.mkdirSync(dir, { recursive: true });
  for (const name of BINARIES) {
    const file = exeName(name, platform);
    const data = findEntry(files, file);
    if (!data) {
      if (name === 'alror') throw new Error(`${archive} does not contain ${file}`);
      continue; // alror-docs is optional
    }
    const dest = path.join(dir, file);
    const tmp = `${dest}.tmp-${process.pid}`;
    fs.writeFileSync(tmp, data, { mode: 0o755 });
    fs.renameSync(tmp, dest);
  }
  fs.writeFileSync(stamp, `${version}\n`);
  return { status: 'downloaded', dir };
}

/** postinstall entry point: never fails the install. */
async function main(o = {}) {
  const env = o.env || process.env;
  const warn = o.warn || ((m) => process.stderr.write(`${m}\n`));
  try {
    const res = await ensureBinary({ ...o, env, log: o.log || warn });
    if (res.status === 'unsupported') {
      warn(`alror: no prebuilt binary for ${platformKey(o.platform, o.arch)}; build from source: https://github.com/${REPO}`);
    } else if (res.status === 'downloaded') {
      warn(`alror: installed binary to ${res.dir}`);
    }
    return res;
  } catch (err) {
    warn(
      `alror: could not download the binary (${err && err.message ? err.message : err}).\n` +
        `alror: it will be retried on first run; or install the platform package, or set ALROR_BINARY_PATH.`,
    );
    return { status: 'failed', error: err };
  }
}

module.exports = { ensureBinary, main, parseChecksums, sha256, vendorDir, truthy, platformPackageInstalled };
