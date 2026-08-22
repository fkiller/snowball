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
let lastConversationUrl = "";
let browserOperation = Promise.resolve();
let projectAnnotationActive = false;
let projectStopRequested = false;
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

function serializeBrowserOperation(operation) {
  const next = browserOperation.then(operation, operation);
  browserOperation = next.then(
    () => undefined,
    () => undefined,
  );
  return next;
}

function conversationUrl(value) {
  try {
    const candidate = new URL(value);
    const chatgpt = new URL(targetUrl);
    if (candidate.origin !== chatgpt.origin || !/^\/c\/[^/]+\/?$/.test(candidate.pathname)) return "";
    candidate.hash = "";
    return candidate.toString();
  } catch {
    return "";
  }
}

function freshChatUrl(value) {
  try {
    const candidate = new URL(value);
    const chatgpt = new URL(targetUrl);
    return candidate.origin === chatgpt.origin && candidate.pathname === chatgpt.pathname &&
      !candidate.search && !candidate.hash;
  } catch {
    return false;
  }
}

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

const voiceSettingsSelectors = [
  'button[data-testid="voice-mode-settings-button"]',
  'button[aria-label*="Voice settings" i]',
  'button[aria-label*="음성 설정" i]',
];

const composerSelectors = [
  '#prompt-textarea',
  '[contenteditable="true"][data-testid="prompt-textarea"]',
  'textarea[placeholder*="Message" i]',
  'textarea[placeholder*="메시지" i]',
];

// ChatGPT exposes dictation separately from full Voice mode. Keep these
// selectors deliberately narrow: a project turn must never click the full
// duplex Voice control by accident.
const dictationStartSelectors = [
  'button[aria-label="Start dictation"]',
  'button[aria-label*="Start dictation" i]',
  'button[aria-label*="음성 입력 시작" i]',
];

const dictationStopSelectors = [
  'button[aria-label="Stop dictation"]',
  'button[aria-label*="Stop dictation" i]',
  'button[aria-label*="음성 입력 중지" i]',
];

const sendMessageSelectors = [
  'button[data-testid="send-button"]',
  'button[aria-label*="Send" i]',
  'button[aria-label*="보내기" i]',
];

const stopGeneratingSelectors = [
  'button[data-testid="stop-button"]',
  'button[aria-label*="Stop generating" i]',
  'button[aria-label*="생성 중지" i]',
];

const readAloudSelectors = [
  'button[data-testid="voice-play-turn-action-button"]',
  'button[aria-label*="Read aloud" i]',
  'button[aria-label*="소리내어 읽기" i]',
];

const stopReadingSelectors = [
  'button[data-testid="voice-stop-turn-action-button"]',
  'button[aria-label*="Stop reading" i]',
  'button[aria-label*="읽기 중지" i]',
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

async function findVisibleWithin(scope, selectors) {
  for (const selector of selectors) {
    const locator = scope.locator(selector).last();
    try {
      if (await locator.isVisible({ timeout: 350 })) return locator;
    } catch {
      // Controls may appear only after the assistant turn is hovered.
    }
  }
  return null;
}

async function findVoicePickerBackButton(dialog) {
  // Recent ChatGPT Voice UI builds close the full-screen picker with a
  // text-only “Back to chat” action instead of the older close-button
  // contract. Keep this exact-text lookup scoped to the picker dialog so a
  // generic page button can never be clicked as a recovery action.
  const buttons = dialog.locator("button");
  const count = Math.min(await buttons.count(), 32);
  for (let index = count - 1; index >= 0; index--) {
    const button = buttons.nth(index);
    if (!(await button.isVisible({ timeout: 250 }).catch(() => false))) continue;
    const text = (await button.innerText().catch(() => "")).trim().replace(/\s+/g, " ");
    if (/^(Back to chat|채팅으로 돌아가기)$/i.test(text)) return button;
  }
  return null;
}

async function dismissBlockingDialog() {
  if (!page) return;

  // ChatGPT can briefly stack the same promotion dialog over another dialog
  // while the Voice page is loading. Always inspect the top-most visible layer
  // and close at most a small bounded number of layers. It is safe to dismiss
  // only when that layer exposes the explicit, supported close contract; never
  // click an arbitrary backdrop or unrelated button.
  for (let attempt = 0; attempt < 3; attempt++) {
    const dialog = page.locator('[role="dialog"][data-state="open"]').last();
    if (!(await dialog.isVisible({ timeout: 250 }).catch(() => false))) {
      if (lastError === "ChatGPT has a blocking dialog without a supported close control.") lastError = "";
      return;
    }
    const close = dialog.locator('button[data-testid="close-button"], button[aria-label="Close"]').last();
    const pickerBack = await findVoicePickerBackButton(dialog);
    if (!(await close.isVisible({ timeout: 250 }).catch(() => false)) && !pickerBack) {
      throw new Error("ChatGPT has a blocking dialog without a supported close control.");
    }
    const dismissButton = await close.isVisible({ timeout: 250 }).catch(() => false) ? close : pickerBack;
    await dismissButton.click({ timeout: 3000 });
    await dialog.waitFor({ state: "hidden", timeout: 3000 }).catch(() => undefined);
    await page.waitForTimeout(75);
  }
  if (lastError === "ChatGPT has a blocking dialog without a supported close control.") lastError = "";
}

async function readRequestJSON(request, maximumBytes = 64 * 1024) {
  const chunks = [];
  let length = 0;
  for await (const chunk of request) {
    length += chunk.length;
    if (length > maximumBytes) throw new Error("Request body is too large.");
    chunks.push(chunk);
  }
  if (length === 0) return {};
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

async function hasAuthenticatedSession() {
  if (!context) return false;
  const cookies = await context.cookies("https://chatgpt.com/");
  return cookies.some(({ name }) =>
    /^__Secure-next-auth\.session-token(?:\.\d+)?$/.test(name),
  );
}

async function frontendLoginShellVisible() {
  // A persistent session can briefly render ChatGPT's logged-out shell while
  // the frontend hydrates after Chromium starts. The cookie check alone is
  // not enough: clicking Voice during that gap opens the anonymous Voice
  // picker and leaves the real session untouched.
  const login = await findVisible(['button[data-testid="login-button"]']);
  const signup = await findVisible(['button[data-testid="signup-button"]']);
  return Boolean(login && signup);
}

async function recoverFrontendLoginShell() {
  if (!(await hasAuthenticatedSession()) || !(await frontendLoginShellVisible())) return;
  try {
    await page.reload({ waitUntil: "domcontentloaded", timeout: 60_000 });
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline && await frontendLoginShellVisible()) {
      await page.waitForTimeout(500);
    }
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
  }
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
	const rememberedConversation = conversationUrl(url);
	if (rememberedConversation) lastConversationUrl = rememberedConversation;

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
  await dismissBlockingDialog();
  await recoverFrontendLoginShell();
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
  lastError = "";
  const deadline = Date.now() + 8000;
  let activeObservations = 0;
  while (Date.now() < deadline) {
    const next = await inspectBrowser();
    if (next.state === "ready" && next.voiceActive) {
      activeObservations += 1;
      if (activeObservations >= 2) return next;
    } else {
      activeObservations = 0;
    }
    await page.waitForTimeout(250);
  }
  throw new Error("ChatGPT did not enter Voice mode. Open Browser Console to inspect the current page.");
}

async function stopVoice() {
  await ensurePage();
  await dismissBlockingDialog();
  const current = await inspectBrowser();
  if (!current.voiceActive) {
    if (current.state === "ready" && current.voiceButtonPresent) {
      lastError = "";
      return current;
    }
    throw new Error(current.reason || "ChatGPT Voice idle state could not be confirmed.");
  }
  const control = await findVisible(stopVoiceSelectors);
  if (!control) throw new Error("ChatGPT Voice is active but its supported End voice control is unavailable.");
  await control.click({ timeout: 3000 });
  const deadline = Date.now() + 5000;
  let idleObservations = 0;
  while (Date.now() < deadline) {
    const next = await inspectBrowser();
    if (next.state === "ready" && next.voiceButtonPresent && !next.voiceActive) {
      idleObservations += 1;
      if (idleObservations >= 2) {
        lastError = "";
        return next;
      }
    } else {
      idleObservations = 0;
    }
    await page?.waitForTimeout(200);
  }
  throw new Error("ChatGPT did not leave Voice mode. Open Browser Console to inspect the current page.");
}

async function waitForIdleVoicePage(label, timeout = 10_000) {
  const deadline = Date.now() + timeout;
  let idleObservations = 0;
  let latest;
  while (Date.now() < deadline) {
    latest = await inspectBrowser();
    if (latest.state === "ready" && latest.authenticated && latest.voiceButtonPresent && !latest.voiceActive) {
      idleObservations += 1;
      if (idleObservations >= 2) return latest;
    } else {
      idleObservations = 0;
    }
    await page?.waitForTimeout(250);
  }
  throw new Error(`${label}: ${latest?.reason || "ChatGPT did not expose a stable idle Voice page."}`);
}

async function navigateNewChat() {
  await ensurePage();
  await dismissBlockingDialog();
  await recoverFrontendLoginShell();
  // The controller keeps one authenticated ChatGPT page warm. After a router
  // restart it is already on the canonical home route, so a second full
  // navigation only adds several seconds of page load before Voice can start.
  // The status inspection is the same authoritative idle check used below;
  // return it directly when the page is already a fresh chat.
  const current = await inspectBrowser();
  if (current.state === "ready" && current.authenticated && current.voiceButtonPresent &&
      !current.voiceActive && freshChatUrl(current.url)) {
    lastError = "";
    return current;
  }
  const previousConversation = conversationUrl(page.url());
  if (previousConversation) lastConversationUrl = previousConversation;
  await page.goto(targetUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
  const status = await waitForIdleVoicePage("ChatGPT did not open a new chat");
  const expected = new URL(targetUrl);
  const actual = new URL(status.url);
  if (actual.origin !== expected.origin || actual.pathname !== expected.pathname || conversationUrl(actual.toString())) {
    throw new Error("ChatGPT did not confirm a fresh new-chat page. The previous conversation was left untouched.");
  }
  lastError = "";
  return status;
}

async function resumeVoice() {
  await ensurePage();
  await dismissBlockingDialog();
  await recoverFrontendLoginShell();
  const current = await inspectBrowser();
  if (current.voiceActive) {
    if (!conversationUrl(current.url)) {
      throw new Error("The active Voice page is not a resumable ChatGPT conversation.");
    }
    return current;
  }
  let resumable = conversationUrl(current.url);
  if (!resumable) resumable = lastConversationUrl;
  if (!resumable) throw new Error("No previous ChatGPT conversation is available to resume.");
  if (page.url() !== resumable) {
    await page.goto(resumable, { waitUntil: "domcontentloaded", timeout: 60_000 });
    await waitForIdleVoicePage("The previous ChatGPT conversation did not become ready");
  }
  return startVoice();
}

async function selectVoice(name) {
  name = String(name || "").trim();
  if (!name || name.length > 64) throw new Error("A valid voice name is required.");
  const current = await inspectBrowser();
  if (!current.voiceActive) throw new Error("Start ChatGPT Voice before selecting a voice.");
  await dismissBlockingDialog();
  await openVoicePicker();

  let candidates = page.locator('[role="radio"], [role="option"]');
  if ((await visibleCandidateNames(candidates, 100)).length === 0) {
    const dialogs = page.locator('[role="dialog"]');
    const scope = (await dialogs.count()) > 0 ? dialogs.last() : page;
    candidates = scope.locator('button[aria-checked], button[data-testid*="voice" i]');
  }
  const count = Math.min(await candidates.count(), 200);
  for (let index = 0; index < count; index++) {
    const candidate = candidates.nth(index);
    const text = (await candidate.innerText().catch(() => "")).trim();
    if (text.localeCompare(name, undefined, { sensitivity: "accent" }) !== 0) continue;
    if (!(await candidate.isVisible().catch(() => false))) continue;
    await candidate.click({ timeout: 3000 });
    return { ...(await inspectBrowser()), selectedVoice: name };
  }
  throw new Error(`Voice “${name}” was not found in the current ChatGPT voice picker.`);
}

async function openVoicePicker() {
  const existing = await visibleCandidateNames(page.locator('[role="radio"], [role="option"]'), 100);
  if (existing.length > 0) return;
  const dialogs = page.locator('[role="dialog"]');
  if ((await dialogs.count()) > 0 && await dialogs.last().isVisible().catch(() => false)) {
    const buttons = await visibleCandidateNames(dialogs.last().locator('button[aria-checked], button[data-testid*="voice" i]'), 100);
    if (buttons.length > 0) return;
  }
  const settingsControl = await findVisible(voiceSettingsSelectors);
  if (!settingsControl) throw new Error("The current ChatGPT Voice UI does not expose a supported voice settings control.");
  await settingsControl.click({ timeout: 3000 });
  await page.waitForTimeout(350);
}

async function closeVoicePicker() {
  const dialogs = page.locator('[role="dialog"]');
  if (await dialogs.count()) {
    const dialog = dialogs.last();
    if (await dialog.isVisible().catch(() => false)) {
      const close = await findVisibleWithin(dialog, [
        'button[data-testid="close-button"]',
        'button[aria-label="Close"]',
        'button[aria-label*="close" i]',
        'button[aria-label*="닫기" i]',
      ]);
      if (close) {
        await close.click({ timeout: 3000 });
        await dialog.waitFor({ state: "hidden", timeout: 3000 }).catch(() => undefined);
        return;
      }
      const pickerBack = await findVoicePickerBackButton(dialog);
      if (pickerBack) {
        await pickerBack.click({ timeout: 3000 });
        await dialog.waitFor({ state: "hidden", timeout: 3000 }).catch(() => undefined);
        lastError = "";
        return;
      }
    }
  }
  // The voice picker is opened by this controller and Escape is scoped to the
  // currently focused picker. If the UI does not expose an explicit close
  // control, verify that Escape actually dismissed it instead of leaving a
  // modal over the live Voice page.
  await page.keyboard.press("Escape");
  await page.waitForTimeout(150);
  const remaining = page.locator('[role="dialog"]');
  if (await remaining.count() && await remaining.last().isVisible().catch(() => false)) {
    throw new Error("ChatGPT voice picker has no supported close control.");
  }
}

async function visibleCandidateNames(locator, maximum) {
  const count = Math.min(await locator.count(), maximum);
  const names = [];
  for (let index = 0; index < count; index++) {
    const candidate = locator.nth(index);
    if (!(await candidate.isVisible().catch(() => false))) continue;
    const text = (await candidate.innerText().catch(() => "")).trim().replace(/\s+/g, " ");
    if (text.length < 2 || text.length > 128) continue;
    if (!names.some((name) => name.localeCompare(text, undefined, { sensitivity: "accent" }) === 0)) names.push(text);
  }
  return names;
}

async function listVoices() {
  const current = await inspectBrowser();
  if (!current.voiceActive) throw new Error("Start ChatGPT Voice before listing voices.");
  await openVoicePicker();
  try {
    let names = await visibleCandidateNames(page.locator('[role="radio"], [role="option"]'), 100);
    if (names.length === 0) {
      const dialogs = page.locator('[role="dialog"]');
      const scope = (await dialogs.count()) > 0 ? dialogs.last() : page;
      names = await visibleCandidateNames(scope.locator('button[aria-checked], button[data-testid*="voice" i]'), 100);
    }
    if (names.length === 0) throw new Error("No supported voice choices are visible in the current picker.");
    return { names };
  } finally {
    await closeVoicePicker();
  }
}

async function listChatGPTProjects() {
  await ensurePage();
  if (!(await hasAuthenticatedSession())) throw new Error("Sign in through Browser Console first.");
  // ChatGPT no longer exposes project homes as stable `/g/g-p-*` anchors.
  // The sidebar keeps a semantic project row and a dedicated "Open project
  // home" button; use that contract first and retain the legacy anchor as a
  // fallback for older deployments.
  const projectRows = page.locator('[data-sidebar-item="true"][class*="project-unfurl-row"]');
  let names = await visibleCandidateNames(projectRows, 250);
  if (names.length === 0) names = await visibleCandidateNames(page.locator('a[href*="/g/g-p-"]'), 250);
  return { names };
}

/* Candidate discovery is deliberately read-only. The Gateway uses this
 * catalog to resolve imperfect English speech against names that are visible
 * in the authenticated ChatGPT session; the ESP32 never ships production
 * project or voice names in firmware. Voice choices are only exposed while
 * the current ChatGPT Voice UI has a supported picker. */
async function listCandidateCatalog() {
  const browser = await inspectBrowser();
  const catalog = {
    version: 1,
    source: "chatgpt-web",
    authenticated: browser.authenticated,
    voiceState: browser.voiceActive ? "available" : "requires_voice",
    voices: [],
    projects: [],
  };
  if (!browser.authenticated) return catalog;

  try {
    catalog.projects = (await listChatGPTProjects()).names;
  } catch (error) {
    lastError = error instanceof Error ? error.message : String(error);
  }
  if (browser.voiceActive) {
    try {
      catalog.voices = (await listVoices()).names;
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error);
    }
  }
  return catalog;
}

async function openChatGPTProject(projectName) {
  projectName = String(projectName || "").trim();
  if (!projectName || projectName.length > 128) throw new Error("A valid ChatGPT project name is required.");
  await ensurePage();
  if (!page.url().startsWith("https://chatgpt.com/")) {
    await page.goto(targetUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
  }

  const projectRows = page.locator('[data-sidebar-item="true"][class*="project-unfurl-row"]');
  const rowCount = await projectRows.count();
  const links = page.locator('a[href*="/g/g-p-"]');
  const count = Math.min(rowCount || await links.count(), 250);
  for (let index = 0; index < count; index++) {
    const row = rowCount ? projectRows.nth(index) : links.nth(index);
    const text = (await row.innerText().catch(() => "")).trim();
    if (text.localeCompare(projectName, undefined, { sensitivity: "accent" }) !== 0) continue;
    if (!(await row.isVisible().catch(() => false))) continue;
    const before = page.url();
    const home = row.locator('button[aria-label="Open project home"]');
    if (await home.count()) await home.click({ timeout: 3000 });
    else await row.click({ timeout: 3000 });
    await page.waitForURL((url) => url.origin === "https://chatgpt.com" && url.toString() !== before, { timeout: 15_000 });
    return;
  }
  throw new Error(`ChatGPT Project “${projectName}” is not visible in the current sidebar. Open it once in Browser Console and try again.`);
}

async function submitProjectTurn({ target, projectName, prompt }) {
  if (target === "codex") {
    throw new Error("Codex local projects require a paired ChatGPT desktop host. The Chromium Web adapter cannot access local Codex folders.");
  }
  if (target !== "chatgpt") throw new Error("Unsupported project target.");
  prompt = String(prompt || "").trim();
  if (!prompt || prompt.length > 32_000) throw new Error("The project request is empty or too long.");
  if (!(await hasAuthenticatedSession())) throw new Error("Sign in through Browser Console first.");

  await openChatGPTProject(projectName);
  let composer = await findVisible(composerSelectors);
  if (!composer) {
    const newChat = await findVisible([
      'a[data-testid="create-new-chat-button"]',
      'button[aria-label*="New chat" i]',
      'button[aria-label*="새 채팅" i]',
    ]);
    if (newChat) {
      await newChat.click({ timeout: 3000 });
      await page.waitForTimeout(500);
      composer = await findVisible(composerSelectors);
    }
  }
  if (!composer) throw new Error("The selected ChatGPT Project does not expose a supported prompt composer.");

  const assistantMessages = page.locator('[data-message-author-role="assistant"]');
  const previousCount = await assistantMessages.count();
  await composer.fill(prompt);
  const send = await findVisible(sendMessageSelectors);
  if (send) await send.click({ timeout: 3000 });
  else await composer.press("Enter");

  const responseDeadline = Date.now() + 180_000;
  let responseText = "";
  let unchangedSince = 0;
  while (Date.now() < responseDeadline) {
    throwIfProjectStopRequested();
    const count = await assistantMessages.count();
    if (count > previousCount) {
      const nextText = (await assistantMessages.last().innerText().catch(() => "")).trim();
      if (nextText && nextText === responseText) {
        if (unchangedSince === 0) unchangedSince = Date.now();
      } else {
        responseText = nextText;
        unchangedSince = Date.now();
      }
      const generating = await findVisible(stopGeneratingSelectors);
      if (responseText && !generating && Date.now() - unchangedSince >= 1200) break;
    }
    await page.waitForTimeout(400);
  }
  if (!responseText) throw new Error("ChatGPT did not produce a project response before the timeout.");

  const message = assistantMessages.last();
  await message.hover().catch(() => undefined);
  const readAloud = await findVisibleWithin(message, readAloudSelectors);
  if (!readAloud) throw new Error("The ChatGPT response does not expose a supported Read aloud control.");
  await readAloud.click({ timeout: 3000 });

  let readingStarted = false;
  const startDeadline = Date.now() + 5000;
  while (Date.now() < startDeadline) {
    if (await findVisibleWithin(message, stopReadingSelectors)) {
      readingStarted = true;
      break;
    }
    await page.waitForTimeout(200);
  }
  let readAloudCompleted = false;
  if (readingStarted) {
    const readingDeadline = Date.now() + 300_000;
    while (Date.now() < readingDeadline) {
      throwIfProjectStopRequested();
      if (!(await findVisibleWithin(message, stopReadingSelectors))) {
        readAloudCompleted = true;
        break;
      }
      await page.waitForTimeout(350);
    }
  }
  return {
    browser: await inspectBrowser(),
    target,
    projectName,
    responseText,
    readAloudStarted: true,
    readAloudCompleted,
  };
}

function normalizedControlText(value) {
  return String(value || "")
    .normalize("NFKC")
    .toLocaleLowerCase()
    .replace(/[“”‘’]/g, "'")
    .replace(/[.!?,，。！？]+$/g, "")
    .trim()
    .replace(/\s+/g, " ");
}

function projectPrompt(template, projectName) {
  return String(template || "").replaceAll("{{projectName}}", projectName);
}

function throwIfProjectStopRequested() {
  if (projectStopRequested) throw new Error("project_session_stopped");
}

async function speakProjectPrompt(text, language) {
  throwIfProjectStopRequested();
  if (!text) throw new Error("project_prompt_empty");
  await page.evaluate(({ prompt, lang }) => new Promise((resolve, reject) => {
    if (!("speechSynthesis" in window) || typeof window.SpeechSynthesisUtterance !== "function") {
      reject(new Error("speech_synthesis_unavailable"));
      return;
    }
    window.speechSynthesis.cancel();
    const utterance = new window.SpeechSynthesisUtterance(prompt);
    utterance.lang = lang || "en-US";
    utterance.rate = 1;
    let settled = false;
    let timer;
    const finish = (callback) => (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      callback(value);
    };
    timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      window.speechSynthesis.cancel();
      reject(new Error("speech_synthesis_timeout"));
    }, 30_000);
    utterance.onend = finish(() => resolve());
    utterance.onerror = finish((event) => reject(new Error(`speech_synthesis_${event.error || "failed"}`)));
    window.speechSynthesis.speak(utterance);
  }), { prompt: text, lang: language });
}

async function composerText(composer) {
  return (await composer.inputValue().catch(async () => composer.textContent().catch(() => ""))).trim();
}

async function stopDictation() {
  const stop = await findVisible(dictationStopSelectors);
  if (stop) {
    await stop.click({ timeout: 3000 });
    await page.waitForTimeout(150);
  }
}

async function captureProjectAnnotation({ projectName, silenceThresholdMs, maxUtteranceMs }) {
  throwIfProjectStopRequested();
  await openChatGPTProject(projectName);
  let composer = await findVisible(composerSelectors);
  if (!composer) {
    const newChat = await findVisible([
      'a[data-testid="create-new-chat-button"]',
      'button[aria-label*="New chat" i]',
      'button[aria-label*="새 채팅" i]',
    ]);
    if (newChat) {
      await newChat.click({ timeout: 3000 });
      await page.waitForTimeout(500);
      composer = await findVisible(composerSelectors);
    }
  }
  if (!composer) throw new Error("project_composer_unavailable");
  if (await composerText(composer)) throw new Error("project_composer_has_pending_text");

  const start = await findVisible(dictationStartSelectors);
  if (!start) throw new Error("project_dictation_unavailable");
  await start.click({ timeout: 3000 });

  const startedAt = Date.now();
  let lastText = "";
  let lastChangedAt = startedAt;
  try {
    while (Date.now() - startedAt < maxUtteranceMs) {
      throwIfProjectStopRequested();
      const nextText = await composerText(composer);
      if (nextText !== lastText) {
        lastText = nextText;
        lastChangedAt = Date.now();
      }
      const dictationStillActive = Boolean(await findVisible(dictationStopSelectors));
      if (lastText && Date.now() - lastChangedAt >= silenceThresholdMs) {
        if (dictationStillActive) await stopDictation();
        return lastText;
      }
      // Some ChatGPT builds hide the stop control immediately after their own
      // VAD decides the utterance is complete. Do not wait for the full
      // maximum duration in that case once text has settled briefly.
      if (lastText && !dictationStillActive && Date.now() - lastChangedAt >= 500) return lastText;
      await page.waitForTimeout(200);
    }
    throw new Error("project_annotation_timeout");
  } finally {
    await stopDictation().catch(() => undefined);
  }
}

async function runProjectAnnotation(input) {
  if (projectAnnotationActive) throw new Error("project_annotation_busy");
  const target = String(input?.target || "").trim().toLocaleLowerCase();
  const projectName = String(input?.projectName || "").trim();
  const language = String(input?.language || "en-US").trim();
  const silenceThresholdMs = Number(input?.silenceThresholdMs);
  const maxUtteranceMs = Number(input?.maxUtteranceMs);
  const readyPrompt = projectPrompt(input?.projectReady, projectName);
  const nextPrompt = projectPrompt(input?.projectNext, projectName);
  const unavailablePrompt = projectPrompt(input?.projectUnavailable, projectName);
  const exitCommands = Array.isArray(input?.exitCommands)
    ? input.exitCommands.map(normalizedControlText).filter(Boolean).slice(0, 16)
    : [];
  if (target === "codex") {
    return { outcome: "not_ready", action: "codex_project_host_unavailable" };
  }
  if (target !== "chatgpt" || !projectName || projectName.length > 128 ||
      !Number.isInteger(silenceThresholdMs) || silenceThresholdMs < 500 || silenceThresholdMs > 10_000 ||
      !Number.isInteger(maxUtteranceMs) || maxUtteranceMs < 5_000 || maxUtteranceMs > 300_000 ||
      !readyPrompt || !nextPrompt || !unavailablePrompt || exitCommands.length === 0) {
    throw new Error("invalid_project_annotation_request");
  }
  if (!(await hasAuthenticatedSession())) throw new Error("Sign in through Browser Console first.");
  projectAnnotationActive = true;
  projectStopRequested = false;
  let turns = 0;
  try {
    await openChatGPTProject(projectName);
    for (let turn = 0; turn < 32; turn++) {
      throwIfProjectStopRequested();
      await speakProjectPrompt(turn === 0 ? readyPrompt : nextPrompt, language);
      const transcript = await captureProjectAnnotation({ projectName, silenceThresholdMs, maxUtteranceMs });
      if (exitCommands.some((command) => normalizedControlText(transcript) === command)) {
        return { outcome: "completed", action: "project_session_exited", projectName, turns };
      }
      throwIfProjectStopRequested();
      await submitProjectTurn({ target, projectName, prompt: transcript });
      turns += 1;
    }
    return { outcome: "not_ready", action: "project_turn_limit_reached", projectName, turns };
  } catch (error) {
    if (projectStopRequested || error?.message === "project_session_stopped") {
      return { outcome: "completed", action: "project_session_stopped", projectName, turns };
    }
    // Keep the unavailable resource user-facing while returning a structured
    // terminal result so the signed device receipt is not left in-flight.
    await speakProjectPrompt(unavailablePrompt, language).catch(() => undefined);
    return {
      outcome: "not_ready",
      action: "project_annotation_not_ready",
      reason: String(error?.message || "project_annotation_failed").slice(0, 256),
      projectName,
      turns,
    };
  } finally {
    await page.evaluate(() => window.speechSynthesis?.cancel()).catch(() => undefined);
    projectAnnotationActive = false;
    projectStopRequested = false;
  }
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
  await recoverFrontendLoginShell();
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
      return sendJson(response, 200, await serializeBrowserOperation(startVoice));
    }
    if (request.method === "POST" && request.url === "/voice/resume") {
      return sendJson(response, 200, await serializeBrowserOperation(resumeVoice));
    }
    if (request.method === "POST" && request.url === "/voice/stop") {
      return sendJson(response, 200, await serializeBrowserOperation(stopVoice));
    }
    if (request.method === "POST" && request.url === "/voice/select") {
      const input = await readRequestJSON(request);
      return sendJson(response, 200, await serializeBrowserOperation(() => selectVoice(input.name)));
    }
    if (request.method === "GET" && request.url === "/voices") {
      return sendJson(response, 200, await serializeBrowserOperation(listVoices));
    }
    if (request.method === "GET" && request.url === "/projects?target=chatgpt") {
      return sendJson(response, 200, await listChatGPTProjects());
    }
    if (request.method === "GET" && request.url === "/candidates") {
      return sendJson(response, 200, await serializeBrowserOperation(listCandidateCatalog));
    }
    if (request.method === "POST" && request.url === "/project/turn") {
      const input = await readRequestJSON(request);
      return sendJson(response, 200, await serializeBrowserOperation(() => submitProjectTurn(input)));
    }
    if (request.method === "POST" && request.url === "/project/annotation") {
      const input = await readRequestJSON(request, 16 << 10);
      return sendJson(response, 200, await serializeBrowserOperation(() => runProjectAnnotation(input)));
    }
    if (request.method === "POST" && request.url === "/project/stop") {
      projectStopRequested = true;
      await page?.evaluate(() => window.speechSynthesis?.cancel()).catch(() => undefined);
      return sendJson(response, 200, { stopping: projectAnnotationActive });
    }
    if (request.method === "POST" && request.url === "/navigate") {
      return sendJson(response, 200, await serializeBrowserOperation(navigateNewChat));
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
