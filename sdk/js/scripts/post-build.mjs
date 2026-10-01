// Mark dist/cjs as CommonJS (the package itself is "type": "module"), so
// Node and TypeScript treat dist/cjs/*.js and *.d.ts as CJS.
import fs from 'node:fs';
fs.writeFileSync(new URL('../dist/cjs/package.json', import.meta.url), '{\n  "type": "commonjs"\n}\n');
