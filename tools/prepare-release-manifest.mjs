import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { readFile, readdir, stat, writeFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import path from "node:path";

const output = path.resolve(process.argv[2] || "artifacts/releases");
const pkg = JSON.parse(await readFile("package.json", "utf8"));
const sha = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
const files = [];
for (const name of (await readdir(output)).sort()) {
  const file = path.join(output, name);
  if (!(await stat(file)).isFile() || name === "release-manifest.json") continue;
  const hash = createHash("sha256");
  if ((await stat(file)).size >= 2000000000) throw new Error(`Asset exceeds GitHub size limit: ${name}`);
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  files.push({ name, bytes: (await stat(file)).size, sha256: hash.digest("hex") });
}
const names = files.map((f) => f.name);
for (const required of [
  `snowball-voice-${pkg.version}-esp32s3.tar.gz`,
  ...["linux-arm64.tar.gz", "linux-amd64.tar.gz", "windows.zip", "macos.tar.gz"].map((suffix) => `snowball-voice-gate-${pkg.version}-${suffix}`),
  ...["arm64", "amd64"].map((arch) => `snowball-voice-gate-${pkg.version}-linux-${arch}-image.tar.gz`),
  ...["arm64", "amd64"].map((arch) => `image-${arch}-sbom.cdx.json`),
  "npm-runtime-sbom.cdx.json", "npm-build-sbom.cdx.json",
]) if (!names.includes(required)) throw new Error(`Missing release asset: ${required}`);
for (const arch of ["arm64", "amd64"]) {
  if (!names.some((n) => n.startsWith(`snowball-voice-gate-${pkg.version}-linux-${arch}-corresponding-source.tar.gz`) && !n.endsWith(".sha256"))) throw new Error(`Missing ${arch} corresponding source`);
}
await writeFile(path.join(output, "release-manifest.json"), JSON.stringify({ version: pkg.version, tag: `v${pkg.version}`, sourceCommit: sha, products: ["Snowball-Voice", "Snowball-Voice-Gate"], protocolVersion: 1, assets: files }, null, 2) + "\n");
console.log(`Verified release asset set: ${files.length} files, source ${sha}`);
