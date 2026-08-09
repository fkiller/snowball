import http from "node:http";
import { chromium } from "playwright";

const listenHost = process.env.BROWSER_CONTROLLER_HOST || "127.0.0.1";
const listenPort = Number(process.env.BROWSER_CONTROLLER_PORT || 3100);
const targetUrl = process.env.CHATGPT_URL || "https://chatgpt.com/";
const cdpEndpoint = process.env.CHROMIUM_CDP_ENDPOINT || "http://127.0.0.1:9222";

let browser;
let context;
let page;
let shuttingDown = false;
const watchedPages = new WeakSet();
let lastError = "";
let lastStatus = {
  state: "starting",
  reason: "Chromium is starting.",
  url: "",
  title: "",
  voiceButtonPresent: false,
  voiceActive: false,
  authenticated: false,
  checkedAt: new Date().toISOString(),
};

const startVoiceSelectors = [
  'button[data-testid="voice-mode-button"]',
  'button[data-testid="composer-speech-button"]',
  'button[aria-label*="Start voice" i]',
  'button[aria-label*="Voice mode" i]',
  'button[aria-label*="음성" i]',
  'button[aria-label*="보이스" i]',
];

const stopVoiceSelectors = [
  'button[data-testid="voice-mode-close-button"]',
  'button[data-testid="end-voice-mode-button"]',
  'button[aria-label*="End voice" i]',
  'button[aria-label*="Exit voice" i]',
  'button[aria-label*="음성 종료" i]',
];

function watchPage(candidate) {
  if (watchedPages.has(candidate)) return;
  watchedPages.add(candidate);
  candidate.on("pageerror", (error) => {
    lastError = error.message;
  });
  candidate.on("close", () => {
    if (page !== candidate) return;
    page = undefined;
    setTimeout(() => void ensurePage().then(() => inspectBrowser()), 250);
  });
}

async function ensurePage() {
  if (!context) return null;
  if (page && !page.isClosed()) return page;

  const pages = context.pages().filter((candidate) => !candidate.isClosed());
  page = pages.at(-1);
  if (!page) {
    page = await context.newPage();
    await page.goto(targetUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
  } else if (page.url() === "about:blank") {
    await page.goto(targetUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
  }
  watchPage(page);
  return page;
}

async function findVisible(selectors) {
  if (!page) return null;
  for (const selector of selectors) {
    const locator = page.locator(selector).last();
    try {
      if (await locator.isVisible({ timeout: 350 })) return locator;
    } catch {
      // A moving UI is expected while ChatGPT loads.
    }
  }
  return null;
}

async function hasAuthenticatedSession() {
  if (!context) return false;
  const cookies = await context.cookies("https://chatgpt.com/");
  return cookies.some(({ name }) =>
    /^__Secure-next-auth\.session-token(?:\.\d+)?$/.test(name),
  );
}

async function inspectBrowser() {
  try {
    await ensurePage();
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
  }

  if (!page || page.isClosed()) {
    lastStatus = {
      state: "starting",
      reason: "Chromium is not ready yet.",
      url: "",
      title: "",
      voiceButtonPresent: false,
      voiceActive: false,
      authenticated: false,
      checkedAt: new Date().toISOString(),
    };
    return lastStatus;
  }

  try {
    const [url, title, voiceButton, stopVoiceButton, authenticated, challengeCount, bodyText] = await Promise.all([
      Promise.resolve(page.url()),
      page.title().catch(() => ""),
      findVisible(startVoiceSelectors),
      findVisible(stopVoiceSelectors),
      hasAuthenticatedSession().catch(() => false),
      page
        .locator('iframe[src*="challenges.cloudflare.com"], iframe[src*="captcha"], [data-testid*="challenge" i]')
        .count()
        .catch(() => 0),
      page.locator("body").innerText({ timeout: 1200 }).catch(() => ""),
    ]);

    let state = "ready";
    let reason = "ChatGPT is ready for voice.";
    const loginLanguage = /\b(log in|sign in)\b|로그인|로그 인/i;
    const challengeLanguage = /verify you are human|security check|captcha|사람인지|보안 확인/i;

    if (url.includes("auth.openai.com") || url.includes("/auth/login") || !authenticated) {
      state = "needs_login";
      reason = "The ChatGPT session needs a login.";
    } else if (challengeCount > 0 || challengeLanguage.test(`${title}\n${bodyText.slice(0, 5000)}`)) {
      state = "needs_human";
      reason = "A browser security challenge is waiting.";
    } else if (!voiceButton && !stopVoiceButton && loginLanguage.test(bodyText.slice(0, 5000))) {
      state = "needs_login";
      reason = "ChatGPT is open without an authenticated session.";
    } else if (stopVoiceButton) {
      reason = "ChatGPT Voice is active.";
    } else if (!voiceButton) {
      state = "needs_human";
      reason = "Snowball could not locate the Voice control. The page has been left untouched.";
    }

    lastStatus = {
      state,
      reason,
      url,
      title,
      voiceButtonPresent: Boolean(voiceButton),
      voiceActive: Boolean(stopVoiceButton),
      authenticated,
      checkedAt: new Date().toISOString(),
      ...(lastError ? { lastError } : {}),
    };
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
    lastStatus = {
      state: "needs_human",
      reason: "The browser stopped responding to the controller.",
      url: page.url(),
      title: "",
      voiceButtonPresent: false,
      voiceActive: false,
      authenticated: false,
      checkedAt: new Date().toISOString(),
      lastError,
    };
  }
  return lastStatus;
}

async function startVoice() {
  await ensurePage();
  const current = await inspectBrowser();
  if (!current.authenticated || current.state === "needs_login") {
    throw new Error("Sign in through Browser Console first. Authentication never starts Voice automatically.");
  }
  if (current.state !== "ready") {
    throw new Error(current.reason || "The browser needs attention before Voice can start.");
  }
  if (current.voiceActive) return current;
  const control = await findVisible(startVoiceSelectors);
  if (!control) {
    await inspectBrowser();
    throw new Error(lastStatus.reason || "Voice control is not available.");
  }
  await control.click({ timeout: 3000 });
  await page.waitForTimeout(750);
  lastError = "";
  return inspectBrowser();
}

async function stopVoice() {
  const control = await findVisible(stopVoiceSelectors);
  if (control) await control.click({ timeout: 3000 });
  await page?.waitForTimeout(400);
  return inspectBrowser();
}

async function connectBrowser() {
  browser = await chromium.connectOverCDP(cdpEndpoint, { timeout: 30_000 });
  context = browser.contexts()[0];
  if (!context) throw new Error("Chromium did not expose its default browser context.");

  browser.on("disconnected", () => {
    browser = undefined;
    context = undefined;
    page = undefined;
    if (!shuttingDown) process.exit(1);
  });
  context.on("page", (candidate) => {
    watchPage(candidate);
    page = candidate;
  });
  for (const candidate of context.pages()) watchPage(candidate);
  await ensurePage();
  await inspectBrowser();
}

function sendJson(response, statusCode, value) {
  response.writeHead(statusCode, {
    "content-type": "application/json; charset=utf-8",
    "cache-control": "no-store",
  });
  response.end(JSON.stringify(value));
}

const server = http.createServer(async (request, response) => {
  try {
    if (request.method === "GET" && request.url === "/status") {
      return sendJson(response, 200, await inspectBrowser());
    }
    if (request.method === "POST" && request.url === "/voice/start") {
      return sendJson(response, 200, await startVoice());
    }
    if (request.method === "POST" && request.url === "/voice/stop") {
      return sendJson(response, 200, await stopVoice());
    }
    if (request.method === "POST" && request.url === "/navigate") {
      await ensurePage();
      await page.goto(targetUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
      return sendJson(response, 200, await inspectBrowser());
    }
    return sendJson(response, 404, { error: "Not found" });
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
    return sendJson(response, 409, { error: lastError, browser: await inspectBrowser() });
  }
});

server.listen(listenPort, listenHost, () => {
  console.log(`browser-controller listening on http://${listenHost}:${listenPort}`);
});

const watchdog = setInterval(() => void inspectBrowser(), 5000);

async function shutdown() {
  shuttingDown = true;
  clearInterval(watchdog);
  server.close();
  process.exit(0);
}

process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);

connectBrowser().catch((error) => {
  lastError = error instanceof Error ? error.message : String(error);
  console.error("Chromium connection failed:", error);
  process.exit(1);
});
