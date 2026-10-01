import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { test, describe, beforeEach } from 'node:test';
import { fileURLToPath } from 'node:url';
import { writeTarGz, writeZip } from '../scripts/lib/archive-write.mjs';

const require = createRequire(import.meta.url);
const alrorDir = fileURLToPath(new URL('../alror/', import.meta.url));
const { ensureBinary, main, parseChecksums, sha256 } = require('../alror/lib/install.js');
const { readTarGz, readZip, readArchive, findEntry } = require('../alror/lib/archive.js');
const { proxyFor, download } = require('../alror/lib/download.js');
const { archiveName, packageName } = require('../alror/lib/platform.js');

const notInstalled = () => {
  throw Object.assign(new Error('not found'), { code: 'MODULE_NOT_FOUND' });
};

function tmpPkg(version = '1.2.3') {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'alror-install-'));
  fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify({ name: 'alror', version }));
  return dir;
}

/** A fake release: archive bytes + checksums.txt, served by a mocked download(). */
function fakeRelease({ version = '1.2.3', key = 'linux-x64', corrupt = false, omitSum = false, entries } = {}) {
  const os_ = key.split('-')[0];
  const exe = (n) => (os_ === 'win32' ? `${n}.exe` : n);
  const files = entries || [
    { name: exe('alror'), data: Buffer.from('#!/bin/sh\necho alror\n') },
    { name: exe('alror-docs'), data: Buffer.from('docs-binary') },
    { name: 'LICENSE', data: Buffer.from('Apache') },
  ];
  const name = archiveName(version, key);
  const archive = name.endsWith('.zip') ? writeZip(files) : writeTarGz(files);
  const sum = corrupt ? 'f'.repeat(64) : sha256(archive);
  const sums = omitSum ? `${'a'.repeat(64)}  other.tar.gz\n` : `${'a'.repeat(64)}  other.tar.gz\n${sum}  ${name}\n`;
  const calls = [];
  const dl = async (url) => {
    calls.push(url);
    if (url.endsWith('/checksums.txt')) return Buffer.from(sums);
    if (url.endsWith(`/${name}`)) return archive;
    throw new Error(`unexpected URL ${url}`);
  };
  return { dl, calls, name, archive };
}

describe('archive readers', () => {
  const files = [
    { name: 'alror', data: Buffer.from('hello') },
    { name: 'nested/dir/alror-docs', data: Buffer.alloc(70000, 7) },
  ];
  test('tar.gz round trip', () => {
    const m = readTarGz(writeTarGz(files));
    assert.equal(m.get('alror').toString(), 'hello');
    assert.equal(findEntry(m, 'alror-docs').length, 70000);
  });
  test('zip round trip, deflate and store', () => {
    const m = readZip(writeZip([...files, { name: 'stored.txt', data: Buffer.from('raw'), store: true }]));
    assert.equal(m.get('alror').toString(), 'hello');
    assert.deepEqual(findEntry(m, 'alror-docs'), Buffer.alloc(70000, 7));
    assert.equal(m.get('stored.txt').toString(), 'raw');
  });
  test('tar with a GNU long name', () => {
    const long = `${'d'.repeat(120)}/alror`;
    const m = readTarGz(writeLongName(long));
    assert.equal(findEntry(m, 'alror').toString(), 'long');
  });
  test('rejects unknown archive types', () => {
    assert.throws(() => readArchive('x.rar', Buffer.alloc(0)), /unsupported archive/);
  });
});

// Build a tar.gz whose first record is a GNU 'L' long-name header.
function writeLongName(long) {
  const zlib = require('node:zlib');
  const header = (name, size, type) => {
    const h = Buffer.alloc(512);
    h.write(name, 0, 100);
    h.write('0000755\0', 100);
    h.write(`${size.toString(8).padStart(11, '0')}\0`, 124);
    h.write(type, 156);
    h.write('ustar\0', 257);
    return h;
  };
  const pad = (n) => Buffer.alloc((512 - (n % 512)) % 512);
  const nameBuf = Buffer.from(`${long}\0`);
  const data = Buffer.from('long');
  return zlib.gzipSync(
    Buffer.concat([header('././@LongLink', nameBuf.length, 'L'), nameBuf, pad(nameBuf.length), header('short', 4, '0'), data, pad(4), Buffer.alloc(1024)]),
  );
}

describe('checksums', () => {
  test('parses GoReleaser checksums.txt', () => {
    const m = parseChecksums(`${'A'.repeat(64)}  alror_1_linux_amd64.tar.gz\r\n${'b'.repeat(64)} *alror_1_windows_amd64.zip\n\ngarbage\n`);
    assert.equal(m.get('alror_1_linux_amd64.tar.gz'), 'a'.repeat(64));
    assert.equal(m.get('alror_1_windows_amd64.zip'), 'b'.repeat(64));
    assert.equal(m.size, 2);
  });
});

describe('ensureBinary (postinstall fallback)', () => {
  let pkgDir;
  beforeEach(() => {
    pkgDir = tmpPkg();
  });

  test('downloads, verifies and extracts when the platform package is missing (tar.gz)', async () => {
    const rel = fakeRelease();
    const res = await ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: {}, download: rel.dl, requireResolve: notInstalled });
    assert.equal(res.status, 'downloaded');
    assert.deepEqual(rel.calls.sort(), [
      'https://github.com/manaskumar3003/alror-cli/releases/download/v1.2.3/alror_1.2.3_linux_amd64.tar.gz',
      'https://github.com/manaskumar3003/alror-cli/releases/download/v1.2.3/checksums.txt',
    ]);
    assert.equal(fs.readFileSync(path.join(pkgDir, 'vendor', 'alror'), 'utf8'), '#!/bin/sh\necho alror\n');
    assert.equal(fs.readFileSync(path.join(pkgDir, 'vendor', 'alror-docs'), 'utf8'), 'docs-binary');
    assert.equal(fs.readFileSync(path.join(pkgDir, 'vendor', '.version'), 'utf8').trim(), '1.2.3');
    if (process.platform !== 'win32') {
      assert.ok(fs.statSync(path.join(pkgDir, 'vendor', 'alror')).mode & 0o111, 'binary is executable');
    }
  });

  test('handles Windows zip archives', async () => {
    const rel = fakeRelease({ key: 'win32-arm64' });
    const res = await ensureBinary({ pkgDir, platform: 'win32', arch: 'arm64', env: {}, download: rel.dl, requireResolve: notInstalled });
    assert.equal(res.status, 'downloaded');
    assert.ok(rel.calls.some((u) => u.endsWith('/alror_1.2.3_windows_arm64.zip')));
    assert.ok(fs.existsSync(path.join(pkgDir, 'vendor', 'alror.exe')));
    assert.ok(fs.existsSync(path.join(pkgDir, 'vendor', 'alror-docs.exe')));
  });

  test('does nothing when the optional platform package is installed', async () => {
    const rel = fakeRelease();
    const seen = [];
    const res = await ensureBinary({
      pkgDir,
      platform: 'linux',
      arch: 'x64',
      env: {},
      download: rel.dl,
      requireResolve: (id) => {
        seen.push(id);
        return '/x/package.json';
      },
    });
    assert.equal(res.status, 'optional-present');
    assert.deepEqual(seen, [`${packageName('linux-x64')}/package.json`]);
    assert.equal(rel.calls.length, 0);
  });

  test('respects ALROR_SKIP_DOWNLOAD', async () => {
    const rel = fakeRelease();
    for (const v of ['1', 'true', 'yes']) {
      const res = await ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: { ALROR_SKIP_DOWNLOAD: v }, download: rel.dl, requireResolve: notInstalled });
      assert.equal(res.status, 'skipped');
    }
    const res = await ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: { ALROR_SKIP_DOWNLOAD: '0' }, download: rel.dl, requireResolve: notInstalled });
    assert.equal(res.status, 'downloaded');
  });

  test('unsupported platforms are reported, not downloaded', async () => {
    const rel = fakeRelease();
    const res = await ensureBinary({ pkgDir, platform: 'freebsd', arch: 'x64', env: {}, download: rel.dl, requireResolve: notInstalled });
    assert.equal(res.status, 'unsupported');
    assert.equal(rel.calls.length, 0);
  });

  test('rejects a checksum mismatch and writes nothing', async () => {
    const rel = fakeRelease({ corrupt: true });
    await assert.rejects(
      ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: {}, download: rel.dl, requireResolve: notInstalled }),
      /checksum mismatch/,
    );
    assert.ok(!fs.existsSync(path.join(pkgDir, 'vendor', 'alror')));
  });

  test('rejects an archive missing from checksums.txt', async () => {
    const rel = fakeRelease({ omitSum: true });
    await assert.rejects(
      ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: {}, download: rel.dl, requireResolve: notInstalled }),
      /no entry for alror_1.2.3_linux_amd64.tar.gz/,
    );
  });

  test('rejects an archive without the alror binary', async () => {
    const rel = fakeRelease({ entries: [{ name: 'README.md', data: Buffer.from('x') }] });
    await assert.rejects(
      ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env: {}, download: rel.dl, requireResolve: notInstalled }),
      /does not contain alror/,
    );
  });

  test('uses ALROR_DOWNLOAD_BASE and caches by version', async () => {
    const rel = fakeRelease();
    const env = { ALROR_DOWNLOAD_BASE: 'https://mirror.example.com/alror/1.2.3/' };
    const first = await ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env, download: rel.dl, requireResolve: notInstalled });
    assert.equal(first.status, 'downloaded');
    assert.ok(rel.calls.every((u) => u.startsWith('https://mirror.example.com/alror/1.2.3/') && !u.includes('//alror_')));
    const second = await ensureBinary({ pkgDir, platform: 'linux', arch: 'x64', env, download: rel.dl, requireResolve: notInstalled });
    assert.equal(second.status, 'cached');
    assert.equal(rel.calls.length, 2);
  });

  test('main() never throws and reports failures as a warning', async () => {
    const warnings = [];
    const res = await main({
      pkgDir,
      platform: 'linux',
      arch: 'x64',
      env: {},
      download: async () => {
        throw new Error('getaddrinfo ENOTFOUND github.com');
      },
      requireResolve: notInstalled,
      warn: (m) => warnings.push(m),
    });
    assert.equal(res.status, 'failed');
    assert.match(warnings.join('\n'), /could not download the binary \(getaddrinfo ENOTFOUND github.com\)/);
  });

  test('install.js exits 0 even when the download fails', async () => {
    const { spawnSync } = await import('node:child_process');
    // A copy of the package with no platform package and an unreachable mirror.
    const dir = tmpPkg('9.9.9');
    fs.cpSync(path.join(alrorDir, 'lib'), path.join(dir, 'lib'), { recursive: true });
    fs.copyFileSync(path.join(alrorDir, 'install.js'), path.join(dir, 'install.js'));
    const r = spawnSync(process.execPath, [path.join(dir, 'install.js')], {
      encoding: 'utf8',
      env: { ...process.env, ALROR_DOWNLOAD_BASE: 'http://127.0.0.1:9/none', ALROR_SKIP_DOWNLOAD: '' },
    });
    assert.equal(r.status, 0, r.stderr);
    if (['linux', 'darwin', 'win32'].includes(process.platform)) assert.match(r.stderr, /could not download/);
  });
});

describe('download()', () => {
  test('follows redirects and returns the body', async () => {
    const server = http.createServer((req, res) => {
      if (req.url === '/start') {
        res.writeHead(302, { Location: '/final' });
        res.end();
      } else if (req.url === '/final') {
        res.end('payload');
      } else {
        res.writeHead(404);
        res.end();
      }
    });
    await new Promise((r) => server.listen(0, '127.0.0.1', r));
    const base = `http://127.0.0.1:${server.address().port}`;
    try {
      assert.equal((await download(`${base}/start`)).toString(), 'payload');
      await assert.rejects(download(`${base}/missing`), /HTTP 404/);
    } finally {
      server.close();
    }
  });

  test('proxyFor honours HTTPS_PROXY and NO_PROXY', () => {
    const target = new URL('https://github.com/x');
    assert.equal(proxyFor(target, {}), null);
    assert.equal(proxyFor(target, { HTTPS_PROXY: 'http://proxy:3128' }).host, 'proxy:3128');
    assert.equal(proxyFor(target, { https_proxy: 'proxy.corp:8080' }).hostname, 'proxy.corp');
    assert.equal(proxyFor(target, { HTTPS_PROXY: 'http://proxy:3128', NO_PROXY: 'example.com,.github.com' }), null);
    assert.equal(proxyFor(target, { HTTPS_PROXY: 'http://proxy:3128', NO_PROXY: '*' }), null);
    assert.equal(proxyFor(target, { HTTPS_PROXY: 'socks5://proxy:1080' }), null);
  });

  test('tunnels through an HTTP CONNECT proxy', async () => {
    // A proxy that accepts CONNECT and then answers itself, standing in for
    // TLS: we only check that the CONNECT is issued with the right target.
    const net = await import('node:net');
    let connectLine = '';
    const proxy = net.createServer((sock) => {
      sock.once('data', (d) => {
        connectLine = d.toString().split('\r\n')[0];
        sock.end('HTTP/1.1 403 Forbidden\r\n\r\n');
      });
    });
    await new Promise((r) => proxy.listen(0, '127.0.0.1', r));
    try {
      await assert.rejects(
        download('https://github.com/manaskumar3003/alror-cli/releases/download/v1/checksums.txt', {
          env: { HTTPS_PROXY: `http://user:pa%20ss@127.0.0.1:${proxy.address().port}` },
        }),
        /proxy CONNECT failed with HTTP 403/,
      );
      assert.equal(connectLine, 'CONNECT github.com:443 HTTP/1.1');
    } finally {
      proxy.close();
    }
  });
});
