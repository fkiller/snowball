import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

const baseURL = process.env.SNOWBALL_TEST_URL || "https://192.168.1.1:8443";
const outputDir = process.env.SNOWBALL_TEST_OUTPUT || "/artifacts";

await mkdir(outputDir, { recursive: true });

async function readStatus(page) {
  return page.evaluate(async () => {
    const response = await fetch("/api/status", { cache: "no-store" });
    return response.json();
  });
}

async function ensureAdminSession(page) {
  const credentials = {
    password: process.env.SNOWBALL_TEST_ADMIN_PASSWORD || "",
    setupCode: process.env.SNOWBALL_TEST_SETUP_CODE || "",
  };
  const auth = await page.evaluate(async () => {
    const response = await fetch("/api/auth/status", { cache: "no-store" });
    return { ok: response.ok, status: response.status, body: await response.json().catch(() => ({})) };
  });
  if (!auth.ok) throw new Error(`Authentication status failed: HTTP ${auth.status}`);
  if (auth.body.authenticated) return;
  if (!credentials.password) {
    throw new Error("Browser smoke needs SNOWBALL_TEST_ADMIN_PASSWORD for an isolated QA administrator session.");
  }
  const endpoint = auth.body.setupRequired ? "/api/auth/bootstrap" : "/api/auth/login";
  if (auth.body.setupRequired && !credentials.setupCode) {
    throw new Error("Browser smoke needs SNOWBALL_TEST_SETUP_CODE for a fresh isolated QA state.");
  }
  const payload = auth.body.setupRequired
    ? { setupCode: credentials.setupCode, password: credentials.password }
    : { password: credentials.password };
  const result = await page.evaluate(async ({ endpoint, payload }) => {
    const response = await fetch(endpoint, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
    });
    return { ok: response.ok, status: response.status, body: await response.json().catch(() => ({})) };
  }, { endpoint, payload });
  if (!result.ok || !result.body.authenticated) {
    throw new Error(`QA administrator authentication failed: HTTP ${result.status}`);
  }
}

async function waitForStatus(page, predicate, label, timeout = 25_000) {
  const deadline = Date.now() + timeout;
  let status;
  while (Date.now() < deadline) {
    status = await readStatus(page);
    if (predicate(status)) return status;
    await page.waitForTimeout(500);
  }
  throw new Error(`${label}: ${JSON.stringify(status)}`);
}

const browser = await chromium.launch({
  executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || "/usr/bin/chromium",
  headless: true,
  args: [
    "--no-sandbox",
    "--ignore-certificate-errors",
    "--use-fake-device-for-media-stream",
    "--use-fake-ui-for-media-stream",
    "--autoplay-policy=no-user-gesture-required",
  ],
});

try {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    permissions: ["microphone"],
    viewport: { width: 1280, height: 900 },
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));

  await page.goto(baseURL, { waitUntil: "networkidle", timeout: 30_000 });
  await ensureAdminSession(page);
  await page.reload({ waitUntil: "networkidle", timeout: 30_000 });
  await page.getByRole("heading", { name: "Talk to your ChatGPT." }).waitFor();
  await page.screenshot({ path: `${outputDir}/snowball-idle.png`, fullPage: true });

  await page.getByRole("button", { name: "Start voice conversation" }).click();
  await page.getByText("Voice is live", { exact: true }).waitFor({ timeout: 25_000 });

  const status = await waitForStatus(
    page,
    (value) => value?.webrtc?.connected && value?.browser?.voiceActive && value?.voiceLive,
    "WebRTC connected but ChatGPT Voice did not become active",
  );

  await page.screenshot({ path: `${outputDir}/snowball-live.png`, fullPage: true });
  await page.getByRole("button", { name: "End voice conversation" }).click();
  await page.getByText("Tap to begin", { exact: true }).waitFor({ timeout: 10_000 });
  await waitForStatus(
    page,
    (value) => !value?.browser?.voiceActive && !value?.voiceLive,
    "ChatGPT Voice did not stop",
    10_000,
  );

  if (errors.length > 0) throw new Error(`Page errors: ${errors.join(" | ")}`);
  console.log(JSON.stringify({ ok: true, webrtc: status.webrtc, browser: status.browser }, null, 2));
} finally {
  await browser.close();
}
