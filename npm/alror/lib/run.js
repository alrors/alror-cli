'use strict';
// Launcher: find the native binary and run it with inherited stdio,
// forwarding signals and the exact exit code (alror uses 2 = rolled back,
// 3 = blocked by the risk gate).

const childProcess = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const { PLATFORMS, platformKey, packageName, exeName, REPO } = require('./platform');

const PKG_DIR = path.join(__dirname, '..');
const OVERRIDE_ENV = { alror: 'ALROR_BINARY_PATH', 'alror-docs': 'ALROR_DOCS_BINARY_PATH' };

/**
 * Locate a binary. Order: env override, optional platform package, vendor/
 * (downloaded by install.js).
 * @returns {string | null}
 */
function resolveBinary(name, o = {}) {
  const env = o.env || process.env;
  const platform = o.platform || process.platform;
  const arch = o.arch || process.arch;
  const pkgDir = o.pkgDir || PKG_DIR;
  const requireResolve = o.requireResolve || require.resolve;
  const file = exeName(name, platform);

  const override = env[OVERRIDE_ENV[name]];
  if (override) return override;

  const key = platformKey(platform, arch);
  if (PLATFORMS[key]) {
    try {
      const pkgJson = requireResolve(`${packageName(key)}/package.json`, { paths: [pkgDir] });
      const candidate = path.join(path.dirname(pkgJson), 'bin', file);
      if (fs.existsSync(candidate)) return candidate;
    } catch {
      // not installed: fall through to vendor/
    }
  }
  const vendored = path.join(pkgDir, 'vendor', file);
  if (fs.existsSync(vendored)) return vendored;
  return null;
}

function ensureExecutable(file) {
  if (process.platform === 'win32') return;
  try {
    fs.accessSync(file, fs.constants.X_OK);
  } catch {
    try {
      fs.chmodSync(file, 0o755);
    } catch {
      // read-only install location: spawn will report the real error
    }
  }
}

const FORWARDED = ['SIGINT', 'SIGTERM', 'SIGHUP', 'SIGQUIT', 'SIGUSR2', 'SIGBREAK'];

/**
 * Spawn the binary and mirror its exit. Resolves with the exit code it set.
 * @param {string} bin
 * @param {string[]} args
 * @param {{exit?: (code: number) => void, platform?: string}} [o]
 */
function exec(bin, args, o = {}) {
  const platform = o.platform || process.platform;
  ensureExecutable(bin);
  return new Promise((resolve) => {
    const child = childProcess.spawn(bin, args, { stdio: 'inherit', windowsHide: false });
    const handlers = new Map();
    for (const sig of FORWARDED) {
      const h = () => {
        // On Windows, Ctrl+C/Ctrl+Break already reach the child through the
        // shared console, and child.kill() would terminate it forcefully.
        if (platform === 'win32' && (sig === 'SIGINT' || sig === 'SIGBREAK')) return;
        try {
          child.kill(sig);
        } catch {
          // child already gone
        }
      };
      try {
        process.on(sig, h);
        handlers.set(sig, h);
      } catch {
        // signal not supported on this platform
      }
    }
    const cleanup = () => {
      for (const [sig, h] of handlers) process.removeListener(sig, h);
    };
    child.on('error', (err) => {
      cleanup();
      process.stderr.write(`alror: failed to start ${bin}: ${err.message}\n`);
      resolve(finish(o, 1));
    });
    child.on('exit', (code, signal) => {
      cleanup();
      if (signal) {
        // Re-raise the signal so our parent sees the same termination cause.
        if (!o.exit) {
          try {
            process.kill(process.pid, signal);
            return; // the process is going down with the signal
          } catch {
            // fall through to the conventional 128+n code
          }
        }
        const n = os.constants.signals[signal] || 1;
        resolve(finish(o, 128 + n));
        return;
      }
      resolve(finish(o, code == null ? 1 : code));
    });
  });
}

function finish(o, code) {
  if (o.exit) o.exit(code);
  else process.exitCode = code;
  return code;
}

/** Entry point used by bin/alror.js and bin/alror-docs.js. */
async function run(name, argv = process.argv.slice(2)) {
  let bin = resolveBinary(name);
  if (!bin) {
    // The postinstall may have been skipped (pnpm/bun without lifecycle
    // scripts, --ignore-scripts): try the download once, now.
    try {
      const res = await require('./install').ensureBinary({
        force: true,
        log: (m) => process.stderr.write(`${m}\n`),
      });
      if (res.status !== 'skipped' && res.status !== 'unsupported') bin = resolveBinary(name);
    } catch (err) {
      process.stderr.write(`alror: download failed: ${err && err.message ? err.message : err}\n`);
    }
  }
  if (!bin) {
    const key = platformKey();
    process.stderr.write(
      [
        `alror: could not find the ${name} binary for ${key}.`,
        PLATFORMS[key]
          ? `  Reinstall without --no-optional / --omit=optional so ${packageName(key)} is installed,`
          : `  There is no prebuilt binary for ${key}.`,
        `  or download it from https://github.com/${REPO}/releases and set ${OVERRIDE_ENV[name]}.`,
        '',
      ].join('\n'),
    );
    process.exitCode = 1;
    return 1;
  }
  return exec(bin, argv);
}

module.exports = { resolveBinary, exec, run };
