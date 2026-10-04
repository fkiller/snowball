import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

test("the local web entry serves Voice and Admin with their hydration assets", async (t) => {
  const port = 31_080;
  const server = spawn(process.execPath, ["services/web-server.mjs"], {
    cwd: new URL("..", import.meta.url),
    env: { ...process.env, WEB_LISTEN_PORT: String(port) },
    stdio: ["ignore", "pipe", "pipe"],
  });
  t.after(() => server.kill("SIGTERM"));
  let startupErrors = "";
  server.stderr.on("data", (chunk) => { startupErrors = (startupErrors + chunk).slice(-16_384); });

  let response;
  for (let attempt = 0; attempt < 50; attempt += 1) {
    try {
      response = await fetch(`http://127.0.0.1:${port}/admin`);
      if (response.ok) break;
    } catch {
      // The server is still starting.
    }
    await delay(100);
  }

  assert.equal(response?.status, 200, `Web startup failed: ${startupErrors}`);
  for (const route of ["/", "/admin"]) {
    const page = await fetch(`http://127.0.0.1:${port}${route}`);
    assert.equal(page.status, 200, `${route} must render`);
    const html = await page.text();
    const source = html.match(/<script[^>]+src="([^"]+\.js)"/)?.[1];
    assert.ok(source, `${route} must reference its client entry JavaScript`);

    const script = await fetch(`http://127.0.0.1:${port}${source}`);
    assert.equal(script.status, 200);
    assert.match(script.headers.get("content-type") || "", /javascript/);
    assert.ok((await script.text()).length > 100);
  }
  const icon = await fetch(`http://127.0.0.1:${port}/branding/icon.png`);
  assert.equal(icon.status, 200, "public assets must remain available");
  assert.match(icon.headers.get("content-type") || "", /image\/png/);
});
