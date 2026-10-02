import { createHash } from "node:crypto";
const [repository, tag, expectedCommit] = process.argv.slice(2);
if (!/^[\w.-]+\/[\w.-]+$/.test(repository || "") || !/^v\d+\.\d+\.\d+-alpha\.\d+$/.test(tag || "") || !/^[a-f0-9]{40}$/.test(expectedCommit || "")) throw new Error("Repository, alpha tag and exact commit are required");
const headers = { "User-Agent": "Snowball-release-verifier", "X-GitHub-Api-Version": "2022-11-28" };
if (process.env.GH_TOKEN) headers.Authorization = `Bearer ${process.env.GH_TOKEN}`;
let response = await fetch(`https://api.github.com/repos/${repository}/releases/tags/${tag}`, { headers });
// GitHub's tag endpoint does not return drafts. Authenticated list lookup does.
let draft;
if (response.status === 404 && process.env.GH_TOKEN) {
  response = await fetch(`https://api.github.com/repos/${repository}/releases?per_page=100`, { headers });
  if (response.ok) draft = (await response.json()).find((r) => r.tag_name === tag && r.draft);
  if (!draft) throw new Error("No authenticated draft for the exact tag");
}
if (!response.ok) throw new Error(`Release metadata HTTP ${response.status}`);
const release = draft || await response.json();
const manifestAsset = release.assets.find((a) => a.name === "release-manifest.json");
if (!manifestAsset) throw new Error("Missing release manifest");
async function download(asset) {
  const response = await fetch(process.env.GH_TOKEN ? asset.url : asset.browser_download_url, { headers: { ...headers, Accept: "application/octet-stream" } });
  if (!response.ok) throw new Error(`Download ${asset.name}: HTTP ${response.status}`);
  return response;
}
const manifest = await (await download(manifestAsset)).json();
if (manifest.tag !== tag || manifest.sourceCommit !== expectedCommit || !release.prerelease) throw new Error("Release tag/commit/prerelease mismatch");
if (release.assets.length !== manifest.assets.length + 1) throw new Error("Published asset count mismatch");
for (const expected of manifest.assets) {
  const asset = release.assets.find((a) => a.name === expected.name);
  if (!asset || asset.size !== expected.bytes) throw new Error(`Asset missing or size mismatch: ${expected.name}`);
  const hash = createHash("sha256");
  let bytes = 0;
  for await (const chunk of (await download(asset)).body) { hash.update(chunk); bytes += chunk.length; }
  if (bytes !== expected.bytes || hash.digest("hex") !== expected.sha256) throw new Error(`Checksum mismatch: ${expected.name}`);
  console.log(`Download verified: ${expected.name} (${bytes} bytes)`);
}
console.log(`All ${manifest.assets.length} downloads verified for ${tag}`);
