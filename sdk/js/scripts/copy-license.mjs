// Ship the repository LICENSE inside the package.
import fs from 'node:fs';
const src = new URL('../../../LICENSE', import.meta.url);
if (fs.existsSync(src)) fs.copyFileSync(src, new URL('../LICENSE', import.meta.url));
