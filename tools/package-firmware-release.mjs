import { createHash } from "node:crypto";
import { cp, mkdir, readFile, readdir, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const project = path.join(root, "firmware", "esp32-s3-audio");
const build = path.resolve(process.argv[2] || path.join(project, "build-release"));
const output = path.resolve(process.argv[3] || path.join(root, "artifacts", "releases"));
const pkg = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
const description = JSON.parse(await readFile(path.join(build, "project_description.json"), "utf8"));
if (description.target !== "esp32s3" || description.project_version !== pkg.version || description.git_revision !== "v5.5.5") {
  throw new Error("Build must be ESP32-S3, ESP-IDF 5.5.5, and match the prepared product version.");
}
const commit = execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
if (execFileSync("git", ["status", "--porcelain", "--untracked-files=no"], { cwd: root, encoding: "utf8" }).trim()) {
  throw new Error("Commit tracked changes before packaging a release.");
}
const images = [
  { offset: "0x0", file: "bootloader/bootloader.bin", maxBytes: 0x8000 },
  { offset: "0x8000", file: "partition_table/partition-table.bin", maxBytes: 0x1000 },
  { offset: "0x10000", file: "snowball_speaker.bin", maxBytes: 0x300000 },
  { offset: "0x310000", file: "srmodels/srmodels.bin", maxBytes: 0x600000 },
];
const args = (await readFile(path.join(build, "flash_args"), "utf8")).trim().split(/\r?\n/);
const ranges = args.filter((line) => /^0x/i.test(line));
if (ranges.length !== images.length || !images.every(({ offset, file }) => ranges.includes(`${offset} ${file}`))) {
  throw new Error("Unexpected flash layout; only the four approved non-NVS images may ship.");
}
const name = `snowball-voice-${pkg.version}-esp32s3`;
const stage = path.join(output, name);
await mkdir(output, { recursive: true });
await mkdir(stage); // Refuse to overwrite an existing release directory.
const hashes = [];
for (const image of images) {
  const source = path.join(build, image.file);
  const bytes = await readFile(source);
  if (!bytes.length) throw new Error(`Empty firmware image: ${image.file}`);
  if (bytes.length > image.maxBytes) throw new Error(`Firmware image overflows its safe non-NVS range: ${image.file}`);
  const target = path.join(stage, image.file);
  await mkdir(path.dirname(target), { recursive: true });
  await cp(source, target);
  image.bytes = bytes.length;
  image.sha256 = createHash("sha256").update(bytes).digest("hex");
  hashes.push(`${image.sha256}  ${image.file}`);
}
await writeFile(path.join(stage, "flash_args"), `${args.join("\n")}\n`);
await writeFile(path.join(stage, "flash-layout.json"), `${JSON.stringify({
  product: "Snowball-Voice", gatewayProduct: "Snowball-Voice-Gate", version: pkg.version,
  protocolVersion: 1, sourceCommit: commit, espIdf: "5.5.5", target: "esp32s3",
  nvsExcluded: true, images,
}, null, 2)}\n`);
for (const file of ["LICENSE", "THIRD_PARTY_NOTICES.md"]) {
  await cp(path.join(root, file), path.join(stage, path.basename(file)));
}
for (const file of ["dependencies.lock", "sdkconfig.defaults"]) {
  await cp(path.join(project, file), path.join(stage, file));
}
await cp(path.join(root, "LICENSES"), path.join(stage, "LICENSES"), { recursive: true });
const notices = [];
async function collect(directory, relative = "", namespace = "managed") {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const rel = path.join(relative, entry.name);
    const source = path.join(directory, entry.name);
    if (entry.isDirectory() && entry.name !== ".git") await collect(source, rel, namespace);
    else if (entry.isFile() && /^(license|copying|notice|copyright)([._-]|$)/i.test(entry.name)) {
      const target = path.join(stage, "LICENSES", namespace, rel);
      await mkdir(path.dirname(target), { recursive: true });
      await cp(source, target);
      notices.push(`${namespace}/${rel.replaceAll(path.sep, "/")}`);
    }
  }
}
await collect(path.join(project, "managed_components"));
if (!notices.some((file) => file.startsWith("managed/espressif__esp_peer/")) || !notices.some((file) => file.startsWith("managed/espressif__esp-sr/"))) {
  throw new Error("Required Espressif component licenses are missing.");
}
if (process.env.SNOWBALL_IDF_NOTICE_DIR) {
  await collect(path.resolve(process.env.SNOWBALL_IDF_NOTICE_DIR), "", "idf");
} else {
  const sdk = process.env.IDF_PATH || description.idf_path;
  await collect(path.join(sdk, "components"), "components", "idf");
  await cp(path.join(sdk, "LICENSE"), path.join(stage, "LICENSES", "idf", "LICENSE"));
}
if (!notices.some((file) => file.includes("lwip") && /copying|license/i.test(file))) throw new Error("ESP-IDF third-party notices are incomplete.");
await writeFile(path.join(stage, "LICENSES", "managed-notices.json"), `${JSON.stringify(notices.sort(), null, 2)}\n`);
await writeFile(path.join(stage, "SHA256SUMS"), `${hashes.join("\n")}\n`);
await writeFile(path.join(stage, "INSTALL.txt"), "Developer preview. Extract this archive into firmware/esp32-s3-audio/build-release of the matching source tag. Use the protected Windows/local flash helper. Never flash or erase NVS at 0x9000. Full instructions and license scope are in the matching source repository docs/GETTING_STARTED.md and THIRD_PARTY_NOTICES.md.\n");
await writeFile(path.join(stage, "INSTALL.md"), `# Snowball-Voice firmware setup\n\nFollow the [complete purchase, tools, archive installation, protected flashing, pairing, and execution guide](https://github.com/fkiller/snowball/blob/${commit}/docs/GETTING_STARTED.md). Use the matching source commit \`${commit}\` and its protected flash helpers. This archive contains no NVS image. Never erase or write NVS at 0x9000.\n`);
const archive = path.join(output, `${name}.tar.gz`);
execFileSync("tar", ["-czf", archive, "-C", output, name]);
const archiveHash = createHash("sha256").update(await readFile(archive)).digest("hex");
await writeFile(`${archive}.sha256`, `${archiveHash}  ${path.basename(archive)}\n`);
console.log(`Packaged ${path.basename(archive)} (${(await stat(archive)).size} bytes), source ${commit}`);
