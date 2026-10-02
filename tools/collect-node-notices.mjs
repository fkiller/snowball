import { cp, mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

const input = path.resolve(process.argv[2] || "node_modules");
const output = path.resolve(process.argv[3] || "third-party/npm");
await mkdir(output, { recursive: true });
const inventory = [];
async function visit(directory, relative = "") {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.isSymbolicLink()) continue;
    const source = path.join(directory, entry.name);
    const rel = path.join(relative, entry.name);
    if (entry.isDirectory()) await visit(source, rel);
    else if (/^(license|copying|notice|copyright)([._-]|$)/i.test(entry.name)) {
      const target = path.join(output, rel);
      await mkdir(path.dirname(target), { recursive: true });
      await cp(source, target);
    } else if (entry.name === "package.json") {
      const pkg = JSON.parse(await readFile(source, "utf8"));
      if (pkg.name && pkg.version) inventory.push({ name: pkg.name, version: pkg.version, license: pkg.license || null, path: rel.replaceAll(path.sep, "/") });
    }
  }
}
await visit(input);
await writeFile(path.join(output, "inventory.json"), JSON.stringify(inventory, null, 2) + "\n");
