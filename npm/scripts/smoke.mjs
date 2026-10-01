#!/usr/bin/env node
// End-to-end smoke test of the npm distribution for the current platform:
// npm pack the launcher and this platform's package, install both tarballs
// into a fresh project and run the CLI through npx.
//
//   node npm/scripts/smoke.mjs [--out npm/out] [--expect-version 1.2.3] [--work dir]

import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { run } from './lib/npm-run.mjs';

const require = createRequire(import.meta.url);
const { platformKey } = require('../alror/lib/platform.js');
const here = path.dirname(fileURLToPath(import.meta.url));

const sh = run;

function must(r, what) {
  if (r.status !== 0) {
    throw new Error(`${what} failed (exit ${r.status})\n${r.stdout}\n${r.stderr}`);
  }
  return r;
}

const { values } = parseArgs({
  options: {
    out: { type: 'string', default: path.join(here, '..', 'out') },
    'expect-version': { type: 'string' },
    work: { type: 'string' },
  },
});
const out = path.resolve(values.out);
const manifest = JSON.parse(fs.readFileSync(path.join(out, 'manifest.json'), 'utf8'));
const key = platformKey();
const work = path.resolve(values.work || fs.mkdtempSync(path.join(os.tmpdir(), 'alror-smoke-')));
const packs = path.join(work, 'packs');
const project = path.join(work, 'project');
fs.mkdirSync(packs, { recursive: true });
fs.mkdirSync(project, { recursive: true });
console.log(`work dir: ${work}`);

// 1. npm pack the launcher and this platform's package.
const tarballs = [];
for (const dir of ['alror', `cli-${key}`]) {
  if (!fs.existsSync(path.join(out, dir))) throw new Error(`${dir} not built in ${out}`);
  const r = must(sh('npm', ['pack', path.join(out, dir), '--pack-destination', packs, '--json']), `npm pack ${dir}`);
  const info = JSON.parse(r.stdout)[0];
  tarballs.push(path.join(packs, info.filename));
  console.log(`packed ${info.name}@${info.version}: ${info.files.map((f) => f.path).join(', ')}`);
}

// 2. Install into a fresh project.
fs.writeFileSync(path.join(project, 'package.json'), JSON.stringify({ name: 'alror-smoke', private: true, version: '1.0.0' }, null, 2));
must(sh('npm', ['install', '--no-audit', '--no-fund', ...tarballs], { cwd: project }), 'npm install');
const env = { ...process.env, ALROR_NO_ANIM: '1', NO_COLOR: '1', ALROR_SKIP_DOWNLOAD: '1' };
delete env.ALROR_SERVER;
delete env.ALROR_API_KEY;
delete env.ALROR_BINARY_PATH;

// 3. Run it.
const ver = must(sh('npx', ['--no-install', 'alror', 'version'], { cwd: project, env }), 'npx alror version');
console.log(`npx alror version -> ${ver.stdout.trim()}`);
assert.match(ver.stdout, /alror \S+/);
if (values['expect-version']) assert.ok(ver.stdout.includes(values['expect-version']), `expected version ${values['expect-version']}`);

const help = must(sh('npx', ['--no-install', 'alror', '--help'], { cwd: project, env }), 'npx alror --help');
assert.match(help.stdout, /Available Commands:/);
console.log('npx alror --help -> ok');

const docsHelp = sh('npx', ['--no-install', 'alror-docs', '--help'], { cwd: project, env, timeout: 15000 });
console.log(`npx alror-docs --help -> exit ${docsHelp.status}`);

must(sh('npx', ['--no-install', 'alror', 'init', '--no-anim'], { cwd: project, env }), 'npx alror init');
const dep = sh('npx', ['--no-install', 'alror', 'deploy', '-s', 'nope', '-i', 'x', '--no-anim'], { cwd: project, env });
console.log(`npx alror deploy -s nope -i x -> exit ${dep.status}: ${(dep.stderr || dep.stdout).trim()}`);
assert.equal(dep.status, 1, 'deploy of an unknown service must exit 1');

// 4. Exit codes 2 and 3 pass through unchanged (node stands in for the binary).
for (const code of [2, 3]) {
  const r = sh('npx', ['--no-install', 'alror', '-e', `process.exit(${code})`], {
    cwd: project,
    env: { ...env, ALROR_BINARY_PATH: process.execPath },
  });
  assert.equal(r.status, code, `exit code ${code} must pass through`);
  console.log(`exit code ${code} -> passed through`);
}

console.log(`smoke test passed (${manifest.version}, ${key})`);
