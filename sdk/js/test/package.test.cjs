// The package works from CommonJS through the "exports" map, and both
// builds expose the same API.
const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs');
const path = require('node:path');

const cjs = require('@alror/sdk');
const pkg = require('../package.json');

test('require() resolves the CJS build', () => {
  assert.equal(require.resolve('@alror/sdk'), path.join(__dirname, '..', 'dist', 'cjs', 'index.js'));
  assert.equal(typeof cjs.Alror, 'function');
  assert.equal(cjs.VERSION, pkg.version, 'src/version.ts must match package.json');
  const err = new cjs.TimeoutError({ code: 'timeout', message: 't' });
  assert.ok(err instanceof cjs.NetworkError && err instanceof cjs.AlrorError && err instanceof Error);
  assert.equal(err.name, 'TimeoutError');
});

test('ESM and CJS builds export the same names', async () => {
  const esm = await import('@alror/sdk');
  assert.deepEqual(Object.keys(esm).filter((k) => k !== 'default').sort(), Object.keys(cjs).filter((k) => k !== '__esModule').sort());
});

test('declarations exist for both builds', () => {
  for (const f of ['dist/esm/index.d.ts', 'dist/cjs/index.d.ts', 'dist/cjs/package.json']) {
    assert.ok(fs.existsSync(path.join(__dirname, '..', f)), f);
  }
  assert.equal(JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'dist/cjs/package.json'), 'utf8')).type, 'commonjs');
});

test('no runtime dependencies', () => {
  assert.equal(pkg.dependencies, undefined);
});
