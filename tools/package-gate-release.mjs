import { createHash } from "node:crypto";
import { mkdir, readFile, unlink, writeFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import path from "node:path";

const root = process.cwd();
const pkg = JSON.parse(await readFile("package.json", "utf8"));
const output = path.resolve(process.argv[2] || "artifacts/releases");
const platforms = ["linux-arm64", "linux-amd64", "windows", "macos"];
const commit = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
if (execFileSync("git", ["status", "--porcelain", "--untracked-files=no"], { encoding: "utf8" }).trim()) throw new Error("Commit tracked changes before release packaging.");
await mkdir(output, { recursive: true });
for (const platform of platforms) {
  const name = `snowball-voice-gate-${pkg.version}-${platform}`;
  const stage = path.join(output, name);
  await mkdir(stage); // No overwriting release archives.
  // Git's tree is the only source: ignored credentials/build/runtime files
  // cannot accidentally enter a platform bundle.
  const source = path.join(output, `${name}-source.tar`);
  execFileSync("git", ["archive", "--format=tar", `--output=${source}`, "HEAD"], { cwd: root });
  execFileSync("tar", ["-xf", source, "-C", stage]);
  await unlink(source);
  await writeFile(path.join(stage, "INSTALL.md"), "# Snowball-Voice-Gate setup\n\nStart with [platform-specific installation](docs/PLATFORMS.md), then [complete setup](docs/GETTING_STARTED.md).\n");
  await writeFile(path.join(stage, "release.json"), `${JSON.stringify({
    product: "Snowball-Voice-Gate", version: pkg.version, sourceCommit: commit,
    platform, runtime: platform.startsWith("linux-") ? "Linux Docker Engine" : "SSH launcher to LAN Linux VM/host",
    protocolVersion: 1, imagePublished: false, instructions: "docs/PLATFORMS.md",
  }, null, 2)}\n`);
  const archive = path.join(output, `${name}.${platform === "windows" ? "zip" : "tar.gz"}`);
  if (platform === "windows") {
    // bsdtar on Windows supports zip. CI uses Python's standard library.
    const python = process.env.SNOWBALL_PACKAGE_PYTHON || (process.platform === "win32" ? "python" : "python3");
    execFileSync(python, ["-c", "import shutil,sys; shutil.make_archive(sys.argv[1], 'zip', sys.argv[2], sys.argv[3])", archive.slice(0, -4), output, name]);
  } else execFileSync("tar", ["-czf", archive, "-C", output, name]);
  const digest = createHash("sha256").update(await readFile(archive)).digest("hex");
  await writeFile(`${archive}.sha256`, `${digest}  ${path.basename(archive)}\n`);
  console.log(`Packaged ${path.basename(archive)}, source ${commit}`);
}
