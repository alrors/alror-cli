#!/usr/bin/env node
'use strict';
// npm launcher for the native `alror` binary. Exit codes are passed through
// unchanged: 0 ok, 1 error, 2 rolled back, 3 blocked by the risk gate.
require('../lib/run').run('alror');
