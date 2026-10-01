#!/usr/bin/env node
// Build ready-to-publish npm package folders from GoReleaser output.
//
//   node npm/scripts/build-packages.mjs --version 1.2.3 --dist dist [--out npm/out]
//                                       [--allow-missing] [--platforms win32-x64,linux-x64]
//
// --dist is GoReleaser's dist/ directory, or any directory holding the release
// archives (alror_<version>_<os>_<arch>.tar.gz|zip) and, ideally, checksums.txt
// (searched recursively). The result is:
//
//   out/cli-<os>-<cpu>/   @alror/cli-<os>-<cpu> with bin/alror(.exe) and bin/alror-docs(.exe)
//   out/alror/            the `alror` launcher with versions synced
//   out/manifest.json     publish order (platforms first)

import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const require = createRequire(import.meta.url);
const here = path.dirname(fileURLToPath(import.meta.url));
const npmDir = path.resolve(here, '..');
const repoRoot = path.resolve(npmDir, '..');

const { PLATFORMS, packageName, exeName, archiveName, stripV } = require('../alror/lib/platform.js');
const { readArchive, findEntry } = require('../alror/lib/archive.js');
const { parseChecksums, sha256 } = require('../alror/lib/install.js');

export async function buildPackages({ version, dist, out, allowMissing = false, platforms, log = console.log }) {
  if (!version) throw new Error('--version is required');
  version = stripV(version);
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`not a semver version: ${version}`);
  }
  if (!dist || !fs.existsSync(dist)) throw new Error(`dist directory not found: ${dist}`);
  out = path.resolve(out || path.join(npmDir, 'out'));
  const keys = platforms && platforms.length ? platforms : Object.keys(PLATFORMS);
  for (const k of keys) if (!PLATFORMS[k]) throw new Error(`unknown platform ${k}`);

  const files = walk(path.resolve(dist), 3);
  const sumsFile = files.find((f) => path.basename(f) === 'checksums.txt');
  const sums = sumsFile ? parseChecksums(fs.readFileSync(sumsFile, 'utf8')) : null;
  if (!sums) log('warning: no checksums.txt found in dist; archives are not verified');

  const license = path.join(repoRoot, 'LICENSE');
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });

  const tmpl = fs.readFileSync(path.join(npmDir, 'platforms', 'package.json.tmpl'), 'utf8');
  const readmeTmpl = fs.readFileSync(path.join(npmDir, 'platforms', 'README.md.tmpl'), 'utf8');
  const built = [];

  for (const key of keys) {
    const [os, cpu] = key.split('-');
    const name = archiveName(version, key);
    const archivePath = files.find((f) => path.basename(f) === name);
    if (!archivePath) {
      if (!allowMissing) throw new Error(`missing archive ${name} in ${dist}`);
      log(`skip ${key}: ${name} not found`);
      continue;
    }
    const buf = fs.readFileSync(archivePath);
    if (sums) {
      const want = sums.get(name);
      if (!want) throw new Error(`checksums.txt has no entry for ${name}`);
      if (sha256(buf) !== want) throw new Error(`checksum mismatch for ${name}`);
    }
    const entries = readArchive(name, buf);
    const dir = path.join(out, `cli-${key}`);
    fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
    for (const bin of ['alror', 'alror-docs']) {
      const file = exeName(bin, os);
      const data = findEntry(entries, file);
      if (!data) throw new Error(`${name} does not contain ${file}`);
      fs.writeFileSync(path.join(dir, 'bin', file), data, { mode: 0o755 });
      fs.chmodSync(path.join(dir, 'bin', file), 0o755);
    }
    const fill = (s) => s.replaceAll('{{key}}', key).replaceAll('{{os}}', os).replaceAll('{{cpu}}', cpu).replaceAll('{{version}}', version);
    const pkg = JSON.parse(fill(tmpl));
    fs.writeFileSync(path.join(dir, 'package.json'), `${JSON.stringify(pkg, null, 2)}\n`);
    fs.writeFileSync(path.join(dir, 'README.md'), fill(readmeTmpl));
    if (fs.existsSync(license)) fs.copyFileSync(license, path.join(dir, 'LICENSE'));
    built.push({ name: pkg.name, dir: path.relative(out, dir).replaceAll('\\', '/') });
    log(`built ${pkg.name}@${version}`);
  }

  // Main package: copy the launcher sources and sync every version.
  const src = path.join(npmDir, 'alror');
  const mainDir = path.join(out, 'alror');
  const mainPkg = JSON.parse(fs.readFileSync(path.join(src, 'package.json'), 'utf8'));
  for (const entry of mainPkg.files) {
    const from = path.join(src, entry);
    if (fs.existsSync(from)) fs.cpSync(from, path.join(mainDir, entry), { recursive: true });
  }
  if (fs.existsSync(license)) fs.copyFileSync(license, path.join(mainDir, 'LICENSE'));
  mainPkg.version = version;
  mainPkg.optionalDependencies = Object.fromEntries(Object.keys(PLATFORMS).sort().map((k) => [packageName(k), version]));
  fs.writeFileSync(path.join(mainDir, 'package.json'), `${JSON.stringify(mainPkg, null, 2)}\n`);
  for (const b of Object.values(mainPkg.bin)) fs.chmodSync(path.join(mainDir, b), 0o755);
  log(`built ${mainPkg.name}@${version}`);

  // Every platform without a package (skipped or filtered out) blocks a real publish.
  const builtNames = new Set(built.map((b) => b.name));
  const manifest = {
    version,
    packages: [...built, { name: mainPkg.name, dir: 'alror' }],
    missing: Object.keys(PLATFORMS).filter((k) => !builtNames.has(packageName(k))),
  };
  fs.writeFileSync(path.join(out, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  return { out, manifest };
}

function walk(dir, depth) {
  const res = [];
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      if (depth > 0) res.push(...walk(p, depth - 1));
    } else res.push(p);
  }
  return res;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      version: { type: 'string' },
      dist: { type: 'string' },
      out: { type: 'string' },
      platforms: { type: 'string' },
      'allow-missing': { type: 'boolean', default: false },
    },
  });
  buildPackages({
    version: values.version || positionals[0] || process.env.VERSION,
    dist: values.dist || positionals[1] || path.join(repoRoot, 'dist'),
    out: values.out,
    allowMissing: values['allow-missing'],
    platforms: values.platforms ? values.platforms.split(',').map((s) => s.trim()).filter(Boolean) : undefined,
  }).then(
    ({ out }) => console.log(`packages written to ${out}`),
    (err) => {
      console.error(`build-packages: ${err.message}`);
      process.exit(1);
    },
  );
}
