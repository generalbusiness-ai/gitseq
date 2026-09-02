'use strict';

const fs = require('fs');
const path = require('path');

if (process.argv.length !== 3) {
  throw new Error('usage: node reference.js JSONATA_GO_MODULE_DIR');
}

const jsonata = require(path.join(process.argv[2], 'v206', 'jsonata-js', 'src', 'jsonata'));
const corpus = JSON.parse(fs.readFileSync(0, 'utf8'));

(async () => {
  const results = {};
  for (const test of corpus.cases) {
    if (test.class === 'environment-dependent') continue;
    results[test.name] = await jsonata(test.expression).evaluate(test.input);
  }
  process.stdout.write(JSON.stringify(results));
})().catch(error => {
  process.stderr.write(String(error && (error.stack || error)));
  process.exitCode = 1;
});
