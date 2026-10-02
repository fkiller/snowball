import { readFile, access } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import path from "node:path";

const files = execFileSync("git", ["ls-files", "-co", "--exclude-standard", "--", "*.md"], { encoding: "utf8" }).trim().split(/\r?\n/);
const failures = [];
for (const file of files) {
  const text = await readFile(file, "utf8");
  for (const match of text.matchAll(/!?\[[^\]]*\]\(([^)]+)\)/g)) {
    const target = match[1].split(/\s+"/)[0].replace(/^<|>$/g, "");
    if (/^(https?:|mailto:|#)/i.test(target)) continue;
    const filename = decodeURIComponent(target.split("#")[0]);
    if (!filename) continue;
    try { await access(path.resolve(path.dirname(file), filename)); }
    catch { failures.push(`${file}: ${target}`); }
  }
}
if (failures.length) { console.error(failures.join("\n")); process.exitCode = 1; }
else console.log(`Local documentation links checked in ${files.length} Markdown files.`);
