import assert from "node:assert/strict";
import test from "node:test";

test("Snowball source contains the primary recovery controls", async () => {
  const page = await import("node:fs/promises").then((fs) => fs.readFile(new URL("../app/voice-console.tsx", import.meta.url), "utf8"));
  assert.match(page, /Start voice conversation/);
  assert.match(page, /Open Browser Console/);
  assert.match(page, /Enable alerts/);
  assert.match(page, /\/api\/webrtc\/offer/);
  assert.match(page, /resize=scale/);
  assert.doesNotMatch(page, /resize=remote/);
  assert.match(page, /two-finger swipe to scroll/);
  assert.match(page, /Signing in never starts Voice/);
});

test("Chromium runs separately from the Playwright controller and is restartable", async () => {
  const fs = await import("node:fs/promises");
  const [launcher, controller, supervisor] = await Promise.all([
    fs.readFile(new URL("../container/start-browser.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../services/browser-controller.mjs", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/supervisord.conf", import.meta.url), "utf8"),
  ]);

  assert.match(launcher, /--remote-debugging-address=127\.0\.0\.1/);
  assert.match(launcher, /--user-data-dir=\/data\/chromium/);
  assert.doesNotMatch(launcher, /enable-automation/);
  assert.match(controller, /connectOverCDP/);
  assert.doesNotMatch(controller, /launchPersistentContext/);
  assert.match(controller, /hasAuthenticatedSession/);
  assert.match(controller, /Authentication never starts Voice automatically/);
  assert.match(supervisor, /\[program:browser-controller\]/);
  assert.match(supervisor, /\[program:browser\][\s\S]*autorestart=true/);
});

test("Chromium receives an exact ChatGPT microphone policy and a real virtual input", async () => {
  const fs = await import("node:fs/promises");
  const [policyText, pulse, launcher] = await Promise.all([
    fs.readFile(new URL("../container/chromium-policy.json", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/pulse/default.pa", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/start-browser.sh", import.meta.url), "utf8"),
  ]);
  const policy = JSON.parse(policyText);

  assert.equal(policy.AudioCaptureAllowed, false);
  assert.deepEqual(policy.AudioCaptureAllowedUrls, [
    "https://chatgpt.com/",
    "https://[*.]chatgpt.com/",
  ]);
  assert.match(pulse, /module-remap-source/);
  assert.match(pulse, /set-default-source chatgpt_mic_source/);
  assert.doesNotMatch(launcher, /use-fake-ui-for-media-stream/);
});

test("The gateway preserves authentication and active Voice state", async () => {
  const gateway = await import("node:fs/promises").then((fs) =>
    fs.readFile(new URL("../gateway/main.go", import.meta.url), "utf8"),
  );
  assert.match(gateway, /VoiceActive\s+bool\s+`json:"voiceActive"`/);
  assert.match(gateway, /Authenticated\s+bool\s+`json:"authenticated"`/);
});
