import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import fastGlob from "fast-glob";

const require = createRequire(import.meta.url);

test("development glob override resolves to the dependency-free-parser adapter", () => {
  assert.equal(require("fast-glob/package.json").name, "@snowball/fast-glob-compat");
  assert.equal(require("fast-glob"), fastGlob);
});

test("Next root discovery and Vite dynamic imports preserve their glob behavior", async (t) => {
  const cwd = await mkdtemp(path.join(os.tmpdir(), "snowball-glob-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  await mkdir(path.join(cwd, "app", "nested"), { recursive: true });
  await mkdir(path.join(cwd, "pages"));
  await Promise.all(["app/a.ts", "app/b.js", "app/skip.ts", "app/.hidden.ts", "app/nested/index.ts", "pages/p.ts"].map((file) => writeFile(path.join(cwd, file), "")));

  const pattern = ["app/*.{ts,js}", "app/*/index.{ts,js}", "!app/skip.ts"];
  const expected = ["app/a.ts", "app/b.js", "app/nested/index.ts"];
  assert.deepEqual(fastGlob.sync(pattern, { cwd }).sort(), expected);
  assert.deepEqual((await fastGlob(pattern, { cwd })).sort(), expected);
  assert.deepEqual(fastGlob.globSync("{app,pages}", { cwd, onlyDirectories: true }).sort(), ["app", "pages"]);
  assert.deepEqual(fastGlob.sync("app", { cwd }), []);
  assert.deepEqual(fastGlob.sync("app/*.ts", { cwd, ignore: ["**/skip.ts"] }), ["app/a.ts"]);
  assert.deepEqual(fastGlob.sync("app/.hidden.ts", { cwd, dot: true }), ["app/.hidden.ts"]);
  assert.equal(path.isAbsolute(fastGlob.sync("pages/*.ts", { cwd, absolute: true })[0]), true);
  assert.throws(() => fastGlob.sync("*", { cwd, objectMode: true }), /Unsupported.*objectMode/);
  assert.doesNotThrow(() => fastGlob.sync("{".repeat(5000) + "a,b" + "}".repeat(5000), { cwd }));
});
