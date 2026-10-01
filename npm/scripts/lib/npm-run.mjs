// Run npm/npx portably. On Windows they are .cmd shims that need a shell, so
// build one quoted command line instead of passing args with shell: true.
import { spawnSync } from 'node:child_process';

const win = process.platform === 'win32';

function quote(s) {
  if (!/[\s"&|<>^()%!]/.test(s)) return s;
  return `"${s.replaceAll('"', '\\"')}"`;
}

/** @param {'npm'|'npx'} cmd @param {string[]} args @param {import('node:child_process').SpawnSyncOptions} [opts] */
export function run(cmd, args, opts = {}) {
  const r = win
    ? spawnSync(`${cmd}.cmd ${args.map(quote).join(' ')}`, { encoding: 'utf8', shell: true, ...opts })
    : spawnSync(cmd, args, { encoding: 'utf8', ...opts });
  if (r.error) throw r.error;
  return r;
}
