#!/usr/bin/env node
// Publish the folders produced by build-packages.mjs, platform packages first
// so the launcher's optionalDependencies resolve the moment it is published.
//
//   node npm/scripts/publish.mjs [--out npm/out] [--dry-run] [--tag next] [--provenance]
//
// Versions that already exist on the registry are skipped, so a failed run
// can simply be re-run. Prerelease versions (1.2.3-rc.1) default to the
// `next` dist-tag; everything else to `latest`. Auth comes from the usual
// npm config (NODE_AUTH_TOKEN with actions/setup-node).

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { run } from './lib/npm-run.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const npm = (args, opts) => run('npm', args, opts);

export function planPublish({ out, dryRun = false, tag, provenance = false }) {
  const manifest = JSON.parse(fs.readFileSync(path.join(out, 'manifest.json'), 'utf8'));
  if (manifest.missing && manifest.missing.length && !dryRun) {
    throw new Error(`refusing to publish: platform packages missing for ${manifest.missing.join(', ')}`);
  }
  const distTag = tag || (manifest.version.includes('-') ? 'next' : 'latest');
  const main = manifest.packages.filter((p) => !p.name.startsWith('@alror/cli-'));
  const platforms = manifest.packages.filter((p) => p.name.startsWith('@alror/cli-'));
  return [...platforms, ...main].map((p) => {
    const args = ['publish', path.join(out, p.dir), '--access', 'public', '--tag', distTag];
    if (dryRun) args.push('--dry-run');
    if (provenance) args.push('--provenance');
    return { name: p.name, version: manifest.version, args };
  });
}

function alreadyPublished(name, version) {
  const r = npm(['view', `${name}@${version}`, 'version']);
  return r.status === 0 && r.stdout.trim() === version;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { values } = parseArgs({
    options: {
      out: { type: 'string', default: path.join(here, '..', 'out') },
      'dry-run': { type: 'boolean', default: false },
      tag: { type: 'string' },
      provenance: { type: 'boolean', default: false },
    },
  });
  let plan;
  try {
    plan = planPublish({ out: path.resolve(values.out), dryRun: values['dry-run'], tag: values.tag, provenance: values.provenance });
  } catch (err) {
    console.error(`publish: ${err.message}`);
    process.exit(1);
  }
  for (const step of plan) {
    if (!values['dry-run'] && alreadyPublished(step.name, step.version)) {
      console.log(`skip ${step.name}@${step.version}: already published`);
      continue;
    }
    console.log(`> npm ${step.args.join(' ')}`);
    const r = npm(step.args, { stdio: 'inherit' });
    if (r.status !== 0) {
      console.error(`publish: npm publish failed for ${step.name}@${step.version}`);
      process.exit(r.status || 1);
    }
  }
}
