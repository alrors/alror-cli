import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, test } from 'node:test';
import { fileURLToPath } from 'node:url';

import { buildPackages } from '../scripts/build-packages.mjs';
import { fakeDist } from '../scripts/fake-dist.mjs';
import { planPublish } from '../scripts/publish.mjs';

const require = createRequire(import.meta.url);
const { resolveBinary, exec } = require('../alror/lib/run.js');
const { readTarGz, findEntry } = require('../alror/lib/archive.js');
const binJs = fileURLToPath(new URL('../alror/bin/alror.js', import.meta.url));

const tmp = (p) => fs.mkdtempSync(path.join(os.tmpdir(), p));

describe('resolveBinary', () => {
  test('prefers ALROR_BINARY_PATH', () => {
    assert.equal(resolveBinary('alror', { env: { ALROR_BINARY_PATH: '/opt/alror' } }), '/opt/alror');
    assert.equal(resolveBinary('alror-docs', { env: { ALROR_DOCS_BINARY_PATH: '/opt/docs' } }), '/opt/docs');
  });

  test('uses the optional platform package, then vendor/', () => {
    const root = tmp('alror-resolve-');
    const platDir = path.join(root, 'node_modules', '@alror', 'cli-linux-arm64');
    fs.mkdirSync(path.join(platDir, 'bin'), { recursive: true });
    fs.writeFileSync(path.join(platDir, 'package.json'), '{}');
    fs.writeFileSync(path.join(platDir, 'bin', 'alror'), 'bin');
    const pkgDir = path.join(root, 'node_modules', 'alror');
    fs.mkdirSync(path.join(pkgDir, 'vendor'), { recursive: true });
    fs.writeFileSync(path.join(pkgDir, 'vendor', 'alror'), 'vendored');

    const requireResolve = (id) => {
      assert.equal(id, '@alror/cli-linux-arm64/package.json');
      return path.join(platDir, 'package.json');
    };
    const o = { env: {}, platform: 'linux', arch: 'arm64', pkgDir, requireResolve };
    assert.equal(resolveBinary('alror', o), path.join(platDir, 'bin', 'alror'));

    const missing = () => {
      throw new Error('MODULE_NOT_FOUND');
    };
    assert.equal(resolveBinary('alror', { ...o, requireResolve: missing }), path.join(pkgDir, 'vendor', 'alror'));
    assert.equal(resolveBinary('alror-docs', { ...o, requireResolve: missing }), null);
  });

  test('looks for .exe on Windows', () => {
    const pkgDir = tmp('alror-win-');
    fs.mkdirSync(path.join(pkgDir, 'vendor'));
    fs.writeFileSync(path.join(pkgDir, 'vendor', 'alror.exe'), 'x');
    const missing = () => {
      throw new Error('nope');
    };
    assert.equal(resolveBinary('alror', { env: {}, platform: 'win32', arch: 'x64', pkgDir, requireResolve: missing }), path.join(pkgDir, 'vendor', 'alror.exe'));
  });
});

describe('exec', () => {
  for (const code of [0, 1, 2, 3, 42]) {
    test(`passes exit code ${code} through`, async () => {
      let exited;
      const got = await exec(process.execPath, ['-e', `process.exit(${code})`], { exit: (c) => (exited = c) });
      assert.equal(got, code);
      assert.equal(exited, code);
    });
  }

  test('reports a binary that cannot start as exit 1', async () => {
    const orig = process.stderr.write;
    process.stderr.write = () => true;
    try {
      const got = await exec(path.join(os.tmpdir(), 'definitely-not-here-alror'), [], { exit: () => {} });
      assert.equal(got, 1);
    } finally {
      process.stderr.write = orig;
    }
  });

  test('maps death by signal to 128+n', { skip: process.platform === 'win32' && 'POSIX signals' }, async () => {
    const got = await exec(process.execPath, ['-e', 'process.kill(process.pid, "SIGTERM")'], { exit: () => {} });
    assert.equal(got, 128 + os.constants.signals.SIGTERM);
  });

  test('bin/alror.js forwards args, stdio and exit codes end to end', () => {
    const env = { ...process.env, ALROR_BINARY_PATH: process.execPath };
    for (const code of [2, 3]) {
      const r = spawnSync(process.execPath, [binJs, '-e', `console.log(process.argv.length); process.exit(${code})`], { env, encoding: 'utf8' });
      assert.equal(r.status, code);
      assert.equal(r.stdout.trim(), '1');
    }
  });

  test('bin/alror.js explains a missing binary', () => {
    const r = spawnSync(process.execPath, [binJs, 'version'], {
      env: { ...process.env, ALROR_BINARY_PATH: '', ALROR_SKIP_DOWNLOAD: '1' },
      encoding: 'utf8',
    });
    assert.equal(r.status, 1);
    assert.match(r.stderr, /could not find the alror binary/);
  });

  test('forwards SIGTERM to the child', { skip: process.platform === 'win32' && 'POSIX signals' }, async () => {
    const { spawn } = await import('node:child_process');
    const script = 'process.on("SIGTERM", () => process.exit(7)); console.log("ready"); setInterval(() => {}, 1000);';
    const p = spawn(process.execPath, [binJs, '-e', script], { env: { ...process.env, ALROR_BINARY_PATH: process.execPath } });
    await new Promise((r) => p.stdout.once('data', r));
    p.kill('SIGTERM');
    const code = await new Promise((r) => p.on('exit', (c) => r(c)));
    assert.equal(code, 7);
  });
});

describe('build-packages', () => {
  const fakeBin = (dir, name, content) => {
    const p = path.join(dir, name);
    fs.writeFileSync(p, content);
    return p;
  };

  test('builds platform packages and the launcher with synced versions', async () => {
    const work = tmp('alror-build-');
    const bins = path.join(work, 'bins');
    fs.mkdirSync(bins);
    const dist = path.join(work, 'dist');
    fakeDist({ version: '1.4.0', platform: 'linux-x64', alror: fakeBin(bins, 'a', 'linux-alror'), docs: fakeBin(bins, 'd', 'linux-docs'), out: dist });
    fakeDist({ version: '1.4.0', platform: 'win32-x64', alror: fakeBin(bins, 'b', 'win-alror'), docs: fakeBin(bins, 'e', 'win-docs'), out: dist });
    const out = path.join(work, 'out');
    const { manifest } = await buildPackages({ version: 'v1.4.0', dist, out, allowMissing: true, log: () => {} });

    assert.equal(manifest.version, '1.4.0');
    assert.deepEqual(manifest.packages.map((p) => p.name), ['@alror/cli-linux-x64', '@alror/cli-win32-x64', 'alror']);
    assert.deepEqual(manifest.missing.sort(), ['darwin-arm64', 'darwin-x64', 'linux-arm64', 'win32-arm64']);

    const lin = JSON.parse(fs.readFileSync(path.join(out, 'cli-linux-x64', 'package.json'), 'utf8'));
    assert.equal(lin.name, '@alror/cli-linux-x64');
    assert.equal(lin.version, '1.4.0');
    assert.deepEqual(lin.os, ['linux']);
    assert.deepEqual(lin.cpu, ['x64']);
    assert.equal(fs.readFileSync(path.join(out, 'cli-linux-x64', 'bin', 'alror'), 'utf8'), 'linux-alror');
    assert.equal(fs.readFileSync(path.join(out, 'cli-linux-x64', 'bin', 'alror-docs'), 'utf8'), 'linux-docs');
    assert.equal(fs.readFileSync(path.join(out, 'cli-win32-x64', 'bin', 'alror.exe'), 'utf8'), 'win-alror');
    assert.ok(fs.existsSync(path.join(out, 'cli-win32-x64', 'LICENSE')));

    const main = JSON.parse(fs.readFileSync(path.join(out, 'alror', 'package.json'), 'utf8'));
    assert.equal(main.version, '1.4.0');
    assert.deepEqual(Object.keys(main.optionalDependencies).sort(), [
      '@alror/cli-darwin-arm64',
      '@alror/cli-darwin-x64',
      '@alror/cli-linux-arm64',
      '@alror/cli-linux-x64',
      '@alror/cli-win32-arm64',
      '@alror/cli-win32-x64',
    ]);
    assert.ok(Object.values(main.optionalDependencies).every((v) => v === '1.4.0'));
    assert.deepEqual(main.bin, { alror: 'bin/alror.js', 'alror-docs': 'bin/alror-docs.js' });
    for (const f of ['bin/alror.js', 'install.js', 'lib/run.js', 'lib/install.js', 'README.md', 'LICENSE']) {
      assert.ok(fs.existsSync(path.join(out, 'alror', f)), f);
    }
  });

  test('fails on a missing archive unless --allow-missing', async () => {
    const work = tmp('alror-build-');
    fs.mkdirSync(path.join(work, 'dist'));
    await assert.rejects(buildPackages({ version: '1.0.0', dist: path.join(work, 'dist'), out: path.join(work, 'out'), log: () => {} }), /missing archive alror_1.0.0_linux_amd64.tar.gz/);
  });

  test('fails on a checksum mismatch', async () => {
    const work = tmp('alror-build-');
    const dist = path.join(work, 'dist');
    const archive = fakeDist({ version: '1.0.0', platform: 'darwin-arm64', alror: fakeBin(work, 'a', 'x'), out: dist });
    fs.appendFileSync(archive, 'tampered');
    await assert.rejects(
      buildPackages({ version: '1.0.0', dist, out: path.join(work, 'out'), allowMissing: true, log: () => {} }),
      /checksum mismatch for alror_1.0.0_darwin_arm64.tar.gz/,
    );
  });

  test('rejects a non-semver version', async () => {
    await assert.rejects(buildPackages({ version: 'latest', dist: '.', log: () => {} }), /not a semver/);
  });
});

describe('publish plan', () => {
  function outWith(version, missing = []) {
    const out = tmp('alror-pub-');
    const packages = [
      { name: '@alror/cli-linux-x64', dir: 'cli-linux-x64' },
      { name: 'alror', dir: 'alror' },
      { name: '@alror/cli-win32-x64', dir: 'cli-win32-x64' },
    ];
    fs.writeFileSync(path.join(out, 'manifest.json'), JSON.stringify({ version, packages, missing }));
    return out;
  }

  test('publishes platform packages first, then the launcher, with --access public', () => {
    const plan = planPublish({ out: outWith('1.0.0') });
    assert.deepEqual(plan.map((p) => p.name), ['@alror/cli-linux-x64', '@alror/cli-win32-x64', 'alror']);
    for (const p of plan) {
      assert.ok(p.args.includes('--access') && p.args[p.args.indexOf('--access') + 1] === 'public');
      assert.equal(p.args[p.args.indexOf('--tag') + 1], 'latest');
      assert.ok(!p.args.includes('--dry-run'));
    }
  });

  test('prereleases go to the next tag; --dry-run and --provenance are passed', () => {
    const plan = planPublish({ out: outWith('1.0.0-rc.1'), dryRun: true, provenance: true });
    for (const p of plan) {
      assert.equal(p.args[p.args.indexOf('--tag') + 1], 'next');
      assert.ok(p.args.includes('--dry-run'));
      assert.ok(p.args.includes('--provenance'));
    }
  });

  test('refuses a real publish when platform packages are missing', () => {
    assert.throws(() => planPublish({ out: outWith('1.0.0', ['darwin-x64']) }), /missing for darwin-x64/);
    assert.equal(planPublish({ out: outWith('1.0.0', ['darwin-x64']), dryRun: true }).length, 3);
  });
});

describe('archive compatibility', () => {
  test('reads a tar.gz produced by the system tar', () => {
    const dir = tmp('alror-systar-');
    fs.writeFileSync(path.join(dir, 'alror'), 'from-system-tar');
    const archive = path.join(dir, 'a.tar.gz');
    const r = spawnSync('tar', ['-czf', 'a.tar.gz', 'alror'], { cwd: dir, encoding: 'utf8' });
    if (r.error || r.status !== 0) return; // no tar on this machine
    const files = readTarGz(fs.readFileSync(archive));
    assert.equal(findEntry(files, 'alror').toString(), 'from-system-tar');
  });
});
