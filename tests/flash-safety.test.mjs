import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, chmod, access, rm } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL("..", import.meta.url));
const project = path.join(root, "firmware", "esp32-s3-audio");
for (const scenario of ["nvs-offset", "partition-overflow"]) {
  test(`protected flashing rejects ${scenario} before any programming tool runs`, { skip: !["linux", "win32"].includes(process.platform) }, async (t) => {
    const build = await mkdtemp(path.join(project, "build-flash-guard-"));
    t.after(async () => {
      assert.ok(path.resolve(build).startsWith(`${path.resolve(project)}${path.sep}`));
      await rm(build, { recursive: true, force: true });
    });
    const files = ["bootloader/bootloader.bin", "partition_table/partition-table.bin", "snowball_speaker.bin", "srmodels/srmodels.bin"];
    for (const file of files) {
      await mkdir(path.dirname(path.join(build, file)), { recursive: true });
      await writeFile(path.join(build, file), Buffer.alloc(file.startsWith("partition") && scenario === "partition-overflow" ? 4097 : 16));
    }
    let args = "--flash_mode dio --flash_freq 80m --flash_size 16MB\n0x0 bootloader/bootloader.bin\n0x8000 partition_table/partition-table.bin\n0x10000 snowball_speaker.bin\n0x310000 srmodels/srmodels.bin\n";
    if (scenario === "nvs-offset") args += "0x9000 nvs.bin\n";
    await writeFile(path.join(build, "flash_args"), args);
    const marker = path.join(build, "programming-called");
    const mock = path.join(build, process.platform === "win32" ? "python-mock.ps1" : "python-mock");
    await writeFile(mock, process.platform === "win32" ? `Set-Content -LiteralPath '${marker.replaceAll("'", "''")}' -Value 'called'\n` : `#!/bin/sh\nprintf called > '${marker}'\n`);
    if (process.platform === "linux") await chmod(mock, 0o755);
    const result = process.platform === "win32"
      ? spawnSync("powershell.exe", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path.join(root, "tools", "flash-esp32-windows.ps1"), "-Port", "COM99", "-BuildDir", build, "-Python", mock], { encoding: "utf8" })
      : spawnSync("sh", [path.join(root, "tools", "flash-esp32-local.sh"), build], { env: { ...process.env, SNOWBALL_SERIAL_PORT: "/dev/ttyACM99", SNOWBALL_IDF_PYTHON: mock }, encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert.match(`${result.stdout}${result.stderr}`, /NVS|safe non-NVS range/i);
    await assert.rejects(access(marker));
  });
}
