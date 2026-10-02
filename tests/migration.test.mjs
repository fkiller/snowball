import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, readFile, chmod, rm } from "node:fs/promises";
import { execFileSync, spawnSync } from "node:child_process";
import path from "node:path";
import os from "node:os";

const linux = process.platform === "linux";
const script = new URL("../openwrt/migrate-to-existing-docker.sh", import.meta.url);
const mock = `#!/usr/bin/env node
const fs=require('fs'),path=require('path');
const p=process.env.MOCK_STATE,s=JSON.parse(fs.readFileSync(p));
const side=process.env.DOCKER_HOST.includes('old')?'old':'new',a=process.argv.slice(2);
s.events.push(side+':'+a.slice(0,2).join(' '));
function end(code=0,text=''){fs.writeFileSync(p,JSON.stringify(s));if(text)process.stdout.write(text+'\\n');process.exit(code)}
if(a[0]==='info')end(0,a.length>1?side+'-daemon':'ready');
if(a[0]==='inspect' && side==='old')end(0,JSON.stringify([{State:{Running:true,Health:{Status:'healthy'}},HostConfig:{NetworkMode:'host'},Config:{User:'pwuser',Hostname:'speaker-test',Env:['SNOWBALL_LAN_IP=192.168.1.20']},Mounts:[{Destination:'/data',Source:s.source}],Image:'sha256:testimage'}]));
if(a[0]==='inspect' && side==='new')end(s.created?0:1,s.created?'healthy':'');
if(a[0]==='volume' && a[1]==='inspect')end(s.volume?0:1,s.volume?s.target:'');
if(a[0]==='volume' && a[1]==='create'){fs.mkdirSync(s.target,{recursive:true});s.volume=true;end()}
if(a[0]==='volume' && a[1]==='rm'){s.volume=false;end()}
if(a[0]==='image'||a[0]==='save'||a[0]==='load')end();
if(a[0]==='stop' && side==='old'){s.oldRunning=false;end()}
if(a[0]==='start' && side==='old'){s.oldRunning=true;end()}
if(a[0]==='create'){if(s.oldRunning)end(42);if(!fs.existsSync(path.join(s.target,'sentinel')))end(43);s.created=true;end()}
if(a[0]==='start' && side==='new')end(s.failStart?1:0);
if(a[0]==='exec'){end(0,a.at(-1).includes('/api/auth/status')?JSON.stringify({setupRequired:false}):JSON.stringify({state:'ready',authenticated:true,voiceActive:false}))}
if(a[0]==='stop' && side==='new')end();
if(a[0]==='rm'){s.created=false;end()}
end(44);
`;

async function fixture(t, failStart = false) {
  const dir = await mkdtemp(path.join(os.tmpdir(), "snowball-migration-test-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const source = path.join(dir, "source", "_data"), target = path.join(dir, "target", "_data");
  await mkdir(source, { recursive: true });
  await writeFile(path.join(source, "sentinel"), "private-state-survives");
  const state = path.join(dir, "state.json"), docker = path.join(dir, "docker-mock");
  await writeFile(state, JSON.stringify({ source, target, oldRunning: true, failStart, events: [] }));
  await writeFile(docker, mock); await chmod(docker, 0o755);
  const env = { ...process.env, MOCK_STATE: state, SNOWBALL_DOCKER_BIN: docker,
    SNOWBALL_MIGRATE_TMP_ROOT: dir, SNOWBALL_OLD_DOCKER_HOST: "unix:///old.sock",
    SNOWBALL_DOCKER_HOST: "unix:///new.sock", SNOWBALL_MIGRATE_APPROVAL: "YES" };
  return { dir, source, target, state, env };
}

test("migration approval guard performs no Docker mutations", { skip: !linux }, async (t) => {
  const f = await fixture(t); delete f.env.SNOWBALL_MIGRATE_APPROVAL;
  const result = spawnSync("sh", [script.pathname], { env: f.env, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.deepEqual(JSON.parse(await readFile(f.state, "utf8")).events, []);
});

test("migration copies stopped state before starting the replacement", { skip: !linux }, async (t) => {
  if (process.getuid() !== 0) return t.skip("migration requires a root-owned isolated mock environment");
  execFileSync("jq", ["--version"]);
  const f = await fixture(t);
  const result = spawnSync("sh", [script.pathname], { env: f.env, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  const state = JSON.parse(await readFile(f.state, "utf8"));
  assert.ok(state.events.indexOf("old:stop --time") < state.events.indexOf("new:create --name"));
  assert.equal(state.oldRunning, false);
  assert.equal(await readFile(path.join(f.source, "sentinel"), "utf8"), "private-state-survives");
  assert.equal(await readFile(path.join(f.target, "sentinel"), "utf8"), "private-state-survives");
});

test("failed replacement restores the old container and preserves its state", { skip: !linux }, async (t) => {
  if (process.getuid() !== 0) return t.skip("migration requires a root-owned isolated mock environment");
  const f = await fixture(t, true);
  const result = spawnSync("sh", [script.pathname], { env: f.env, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  const state = JSON.parse(await readFile(f.state, "utf8"));
  assert.equal(state.oldRunning, true); assert.equal(state.created, false);
  assert.equal(state.volume, false);
  assert.equal(await readFile(path.join(f.source, "sentinel"), "utf8"), "private-state-survives");
});
