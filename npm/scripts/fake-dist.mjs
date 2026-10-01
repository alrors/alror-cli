#!/usr/bin/env node
// Fake a GoReleaser dist/ for one platform from locally built binaries, so
// the npm packaging can be tested without a release.
//
//   node npm/scripts/fake-dist.mjs --version 0.1.0-local --platform win32-x64 \
//     --alror bin/alror.exe --docs bin/alror-docs.exe --out dist-local
//
// Writes dist-local/alror_<version>_<goos>_<goarch>.(tar.gz|zip) and checksums.txt.

import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { writeTarGz, writeZip } from './lib/archive-write.mjs';

const require = createRequire(import.meta.url);
const { PLATFORMS, archiveName, exeName, platformKey } = require('../alror/lib/platform.js');
const { sha256 } = require('../alror/lib/install.js');

export function fakeDist({ version, platform = platformKey(), alror, docs, out }) {
  if (!PLATFORMS[platform]) throw new Error(`unknown platform ${platform}`);
  const os = platform.split('-')[0];
  const entries = [
    { name: exeName('alror', os), data: fs.readFileSync(alror) },
    { name: exeName('alror-docs', os), data: fs.readFileSync(docs || alror) },
    { name: 'LICENSE', data: Buffer.from('Apache-2.0\n'), mode: 0o644 },
  ];
  const name = archiveName(version, platform);
  const buf = PLATFORMS[platform].ext === 'zip' ? writeZip(entries) : writeTarGz(entries);
  fs.mkdirSync(out, { recursive: true });
  fs.writeFileSync(path.join(out, name), buf);
  const sumsPath = path.join(out, 'checksums.txt');
  const prev = fs.existsSync(sumsPath) ? fs.readFileSync(sumsPath, 'utf8').split('\n').filter((l) => l && !l.endsWith(` ${name}`)) : [];
  fs.writeFileSync(sumsPath, `${[...prev, `${sha256(buf)}  ${name}`].join('\n')}\n`);
  return path.join(out, name);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { values } = parseArgs({
    options: {
      version: { type: 'string' },
      platform: { type: 'string' },
      alror: { type: 'string' },
      docs: { type: 'string' },
      out: { type: 'string', default: 'dist-local' },
    },
  });
  if (!values.version || !values.alror) {
    console.error('usage: fake-dist.mjs --version X --alror path [--docs path] [--platform win32-x64] [--out dir]');
    process.exit(2);
  }
  console.log(fakeDist({ ...values, platform: values.platform || platformKey() }));
}
