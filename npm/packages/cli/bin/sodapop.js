#!/usr/bin/env node

"use strict";

const { main } = require("../lib/launcher");

main(process.argv.slice(2)).then((code) => { process.exitCode = code; }).catch((error) => {
  console.error(`sodapop: ${error.message}`);
  process.exitCode = 1;
});
