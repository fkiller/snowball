import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("../tools/deploy-candidate.sh", import.meta.url));
const linux = process.platform === "linux";
const docker = `#!/bin/sh
printf '%s\\n' "$*" >> "$MOCK_LOG"
case "$1" in
    image|tag|stop|rm|rename) exit 0 ;;
    inspect)
        case "$2" in
            -f) case "$3" in
                *Config.Image*) echo 'snowball-voice:original' ;;
                '{{.State.Status}}') echo running ;;
                *State.Health*) echo healthy ;;
                *'.Type'*) echo volume ;;
                *'.Name'*) echo snowball-voice-data ;;
                *) exit 90 ;;
            esac ;;
            snowball-voice) exit 0 ;;
            *) exit 1 ;;
        esac ;;
    create) [ "$MOCK_FAILURE" != create ] || exit 1; echo candidate-id ;;
    start) [ "$2" != snowball-voice ] || [ "$MOCK_FAILURE" != rollback ] ;;
    exec) echo '{"state":"ready","authenticated":true,"voiceActive":false}' ;;
    *) exit 90 ;;
esac
`;
const curl = `#!/bin/sh
for target do :; done
case "$target" in
    */api/health) echo '{"ok":true}' ;;
    */api/auth/status) echo '{"setupRequired":false}' ;;
    */admin) if [ "$MOCK_FAILURE" = none ]; then echo 200; else echo 500; fi ;;
    */console/) echo 401 ;;
    *) exit 90 ;;
esac
`;

async function deploy(t, failure) {
  const dir = await mkdtemp(path.join(os.tmpdir(), "snowball-deploy-test-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const log = path.join(dir, "docker.log");
  await writeFile(path.join(dir, "docker"), docker, { mode: 0o755 });
  await writeFile(path.join(dir, "curl"), curl, { mode: 0o755 });
  const result = spawnSync("sh", [script], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${dir}:${process.env.PATH}`,
      MOCK_LOG: log,
      MOCK_FAILURE: failure,
      SNOWBALL_CONTAINER: "snowball-voice",
      SNOWBALL_CANDIDATE_IMAGE: "snowball-voice:candidate",
      SNOWBALL_DEPLOY_IMAGE: "snowball-voice:release",
      SNOWBALL_DEPLOY_APPROVAL: "YES",
    },
  });
  return { ...result, commands: await readFile(log, "utf8") };
}

test("candidate reuses the original named state volume", { skip: !linux }, async (t) => {
  const result = await deploy(t, "none");
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.commands, /--mount type=volume,src=snowball-voice-data,dst=\/data/);
  assert.match(result.commands, /start candidate-id/);
});

test("failed deployment removes only its candidate and verifies rollback", { skip: !linux }, async (t) => {
  const result = await deploy(t, "probe");
  assert.equal(result.status, 1);
  assert.match(result.commands, /rm candidate-id/);
  assert.doesNotMatch(result.commands, /^rm snowball-voice/m);
  assert.match(result.stderr, /rollback verified: snowball-voice is healthy/);
});

test("failed creation can roll back without removing a container by name", { skip: !linux }, async (t) => {
  const result = await deploy(t, "create");
  assert.equal(result.status, 1);
  assert.doesNotMatch(result.commands, /^rm /m);
  assert.match(result.stderr, /rollback verified: snowball-voice is healthy/);
});

test("failed restoration is reported as a failure", { skip: !linux }, async (t) => {
  const result = await deploy(t, "rollback");
  assert.equal(result.status, 1);
  assert.match(result.stderr, /rollback failed:/);
  assert.doesNotMatch(result.stderr, /rollback verified/);
});
