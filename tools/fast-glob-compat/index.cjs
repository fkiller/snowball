/* eslint @typescript-eslint/no-require-imports: "off" -- The upstream consumers require CommonJS. */
const { glob, globSync } = require("tinyglobby");

// Next lint uses onlyDirectories; Vite dynamic imports use cwd and brace lists.
// Reject new options rather than silently promising the full fast-glob API.
function optionsFor(options = {}) {
  for (const key of Object.keys(options)) {
    if (!["cwd", "onlyDirectories", "absolute", "ignore", "dot"].includes(key)) {
      throw new TypeError(`Unsupported fast-glob adapter option: ${key}`);
    }
  }
  return { ...options, expandDirectories: false };
}

function find(patterns, options) {
  return glob(patterns, optionsFor(options)).then(normalizePaths);
}

function findSync(patterns, options) {
  return normalizePaths(globSync(patterns, optionsFor(options)));
}

// tinyglobby appends a slash to directories; fast-glob returns unmarked paths.
function normalizePaths(entries) {
  return entries.map((entry) => entry.replace(/([^/:])\/$/, "$1"));
}

module.exports = Object.assign(find, { glob: find, sync: findSync, globSync: findSync });
