#!/usr/bin/env node
'use strict';
// postinstall: fetch the binary only when the optional platform package is
// missing. Always exits 0 so it never breaks `npm install` (CI included).
// Set ALROR_SKIP_DOWNLOAD=1 to skip it entirely.

require('./lib/install')
  .main()
  .then(
    () => process.exit(0),
    () => process.exit(0),
  );
