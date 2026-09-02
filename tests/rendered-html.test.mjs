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
  assert.match(page, /operationRef/);
  assert.match(page, /Conversation ended in ChatGPT/);
  assert.match(page, /waitForBrowserVoiceState/);
  assert.match(page, /ChatGPT Voice is still live\. Snowball could not confirm the stop/);
  assert.doesNotMatch(page, /finally \{\s*setSession\("idle"\)/);
});

test("Chromium runs separately from the Playwright controller and is restartable", async () => {
  const fs = await import("node:fs/promises");
  const [launcher, controller, supervisor, prepare] = await Promise.all([
    fs.readFile(new URL("../container/start-browser.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../services/browser-controller.mjs", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/supervisord.conf", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/prepare-runtime.sh", import.meta.url), "utf8"),
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
  assert.match(prepare, /flock -x 9/);
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
  assert.match(gateway, /authoritativeVoiceLive/);
  assert.match(gateway, /"voiceLive": voiceLive/);
});

test("Snowball protects control APIs and the live browser console", async () => {
  const fs = await import("node:fs/promises");
  const [gateway, auth, nginx, client] = await Promise.all([
    fs.readFile(new URL("../gateway/main.go", import.meta.url), "utf8"),
    fs.readFile(new URL("../gateway/auth.go", import.meta.url), "utf8"),
    fs.readFile(new URL("../container/nginx.conf.template", import.meta.url), "utf8"),
    fs.readFile(new URL("../app/auth-boundary.tsx", import.meta.url), "utf8"),
  ]);
  assert.match(gateway, /g\.protect\(g\.handleOffer, true\)/);
  assert.match(gateway, /g\.protect\(g\.handleSettings, true\)/);
  assert.match(auth, /__Host-snowball_session/);
  assert.match(auth, /SameSiteStrictMode/);
  assert.match(auth, /X-CSRF-Token/);
  assert.match(nginx, /location \/console\/ \{[\s\S]*auth_request \/api\/auth\/authorize/);
  assert.match(client, /X-CSRF-Token/);
});

test("Security automation is pinned and blocks vulnerable runtime dependencies", async () => {
  const workflow = await import("node:fs/promises").then((fs) =>
    fs.readFile(new URL("../.github/workflows/security-gate.yml", import.meta.url), "utf8"),
  );
  assert.match(workflow, /npm audit --omit=dev --audit-level=high/);
  assert.match(workflow, /govulncheck \.\/\.\.\./);
  assert.match(workflow, /espressif\/idf:v5\.5\.5/);
  assert.match(workflow, /trivy-action@[a-f0-9]{40}/);
  assert.doesNotMatch(workflow, /uses: [^\n]+@(main|master|v\d+)\s*$/m);
});

test("ChatGPT project discovery supports the current semantic sidebar rows", async () => {
  const controller = await import("node:fs/promises").then((fs) =>
    fs.readFile(new URL("../services/browser-controller.mjs", import.meta.url), "utf8"),
  );
  assert.match(controller, /data-sidebar-item="true"\]\[class\*="project-unfurl-row"/);
  assert.match(controller, /Open project home/);
  assert.match(controller, /url\.toString\(\) !== before/);
  assert.match(controller, /listCandidateCatalog/);
  assert.match(controller, /request\.url === "\/candidates"/);
  assert.match(controller, /closeVoicePicker/);
  assert.match(controller, /button\[aria-label="Start dictation"\]/);
  assert.match(controller, /speechSynthesis/);
  assert.match(controller, /runProjectAnnotation/);
  assert.match(controller, /request\.url === "\/project\/annotation"/);
  assert.match(controller, /request\.url === "\/project\/stop"/);
});

test("Gateway candidate and project capability responses are explicit", async () => {
  const [gateway, devices] = await Promise.all([
    import("node:fs/promises").then((fs) => fs.readFile(new URL("../gateway/main.go", import.meta.url), "utf8")),
    import("node:fs/promises").then((fs) => fs.readFile(new URL("../gateway/devices.go", import.meta.url), "utf8")),
  ]);
  assert.match(gateway, /GET \/api\/candidates/);
  assert.match(gateway, /candidates_synchronized/);
  assert.match(gateway, /deviceCandidateCatalog\(catalog\)/);
  assert.match(gateway, /chatgpt_project_turn_ready/);
  assert.match(gateway, /project_turn_completed/);
  assert.match(gateway, /peerDeviceProjectStarted/);
  assert.match(gateway, /codex_project_host_unavailable/);
  assert.match(devices, /input\.Event != "sync"/);
});

test("Admin exposes the authenticated candidate snapshot without device secrets", async () => {
  const admin = await import("node:fs/promises").then((fs) => fs.readFile(new URL("../app/admin/admin-console.tsx", import.meta.url), "utf8"));
  assert.match(admin, /\/api\/candidates/);
  assert.match(admin, /Visible ChatGPT names/);
  assert.match(admin, /bounded signed snapshot/);
  assert.doesNotMatch(admin, /hardwareId.*candidate/);
});

test("USB provisioning stays in the authenticated browser boundary", async () => {
  const fs = await import("node:fs/promises");
  const [pairing, gateway, compose] = await Promise.all([
    fs.readFile(new URL("../app/admin/device-pairing.tsx", import.meta.url), "utf8"),
    fs.readFile(new URL("../gateway/main.go", import.meta.url), "utf8"),
    fs.readFile(new URL("../compose.yaml", import.meta.url), "utf8"),
  ]);
  assert.match(pairing, /navigator as SerialNavigator/);
  assert.match(pairing, /usbVendorId: 0x303a/);
  assert.match(pairing, /\/api\/devices\/enrollment/);
  assert.match(pairing, /setPassword\(""\)/);
  assert.match(gateway, /g\.protect\(g\.handleDeviceEnrollment, true\)/);
  assert.doesNotMatch(compose, /\/dev\/tty|devices:/);
});

test("Device control events require signed replay-protected authentication", async () => {
  const fs = await import("node:fs/promises");
  const [devices, gateway, firmware, roadmap] = await Promise.all([
    fs.readFile(new URL("../gateway/devices.go", import.meta.url), "utf8"),
    fs.readFile(new URL("../gateway/main.go", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/provisioning.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../docs/ROADMAP.md", import.meta.url), "utf8"),
  ]);
  assert.match(devices, /snowball-device-event-v1/);
  assert.match(devices, /device event replay rejected/);
  assert.match(gateway, /POST \/api\/device\/events/);
  assert.match(gateway, /device_command_processing/);
  assert.match(gateway, /scheduleDeviceDispatch/);
  assert.match(firmware, /DEVICE_EVENT_PROCESSING_ATTEMPTS/);
  assert.match(firmware, /device event still processing/);
  assert.match(roadmap, /Authenticated device control session/);
});

test("Hardware scenarios gate Voice on pairing and authoritative full-duplex state", async () => {
  const scenarios = await import("node:fs/promises").then((fs) => fs.readFile(new URL("../docs/TEST_SCENARIOS.md", import.meta.url), "utf8"));
  assert.match(scenarios, /Hi ESP Resume/);
  assert.match(scenarios, /Hi ESP Codex Project Snowball/);
  assert.match(scenarios, /WebRTC is connected/);
  assert.match(scenarios, /Pairing acceptance/);
  assert.match(scenarios, /`Hi ESP` full-duplex and command-tail baseline/);
  assert.match(scenarios, /voiceActive:true/);
  assert.match(scenarios, /Wake, a chime, or WebRTC alone cannot satisfy/);
  assert.match(scenarios, /ESP_ERR_INVALID_RESPONSE/);
  assert.match(scenarios, /DOWNLOAD\(USB\/UART0\)/);
});

test("ESP32 diagnostics expose recognition, delivery, and bounded host logging", async () => {
  const fs = await import("node:fs/promises");
  const [pairing, firmware, speech, audio, commandRecognizer, provisioning, logger, shellFramer, boardHelper, debugTool, traceReport, flasher, scenarios, browserController] = await Promise.all([
    fs.readFile(new URL("../app/admin/device-pairing.tsx", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/app_main.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/speech.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/board_audio.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/command_recognizer.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../firmware/esp32-s3-audio/main/provisioning.c", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/esp32-serial-logger.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/esp32-log-framer.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/snowball-board.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/snowball-esp32-debug", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/esp32-voice-trace-report.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../tools/flash-esp32.sh", import.meta.url), "utf8"),
    fs.readFile(new URL("../docs/TEST_SCENARIOS.md", import.meta.url), "utf8"),
    fs.readFile(new URL("../services/browser-controller.mjs", import.meta.url), "utf8"),
  ]);
  assert.match(pairing, /sendOperation\("trace"\)/);
  assert.match(pairing, /sendOperation\("debug-mode"/);
  assert.match(pairing, /Recent device trace/);
  assert.match(pairing, /device\.state === "active"/);
  assert.match(pairing, /gatewayReachable/);
  assert.match(firmware, /trace_event\("wake_detected"/);
  assert.match(firmware, /trace_event\("media_connected"/);
  assert.match(firmware, /trace_event\("voice_started"/);
  assert.match(firmware, /SNOWBALL_FEEDBACK_ACTION_NOT_READY/);
  assert.match(firmware, /esp_reset_reason\(\)/);
  assert.match(firmware, /event.*reset/);
  assert.match(firmware, /PSRAM cache-safety enabled/);
  assert.match(speech, /board_audio_begin_wake/);
  assert.match(speech, /board_audio_peek_levels/);
  assert.match(speech, /audio_level/);
  assert.match(speech, /resolve_command_frame/);
  assert.match(speech, /WAKENET_DETECTED/);
  assert.match(speech, /WAKENET_CHANNEL_VERIFIED/);
  assert.match(speech, /vTaskDelay\(1\)/);
  assert.match(speech, /config->aec_init = false/);
  assert.match(speech, /config->se_init = false/);
  assert.match(speech, /AFE AEC initialized/);
  assert.match(speech, /AFE fetch duration/);
  assert.match(speech, /post_session_generation/);
  assert.match(speech, /post_session_partial_wake_rejected/);
  assert.match(speech, /conversation_partial_wake_rejected/);
  assert.match(speech, /WAKENET_CHANNEL_VERIFIED/);
  assert.match(speech, /VOICE_STATE_IDLE/);
  assert.match(speech, /VOICE_STATE_COMMAND_WINDOW/);
  assert.match(speech, /VOICE_STATE_CONNECTING/);
  assert.match(speech, /VOICE_STATE_CONVERSATION/);
  assert.match(speech, /VOICE_STATE_ENDING/);
  assert.match(speech, /hi_esp_end/);
  assert.match(speech, /on_end_session/);
  assert.match(speech, /speech_session_activated/);
  assert.match(audio, /feedback_generation/);
  assert.match(audio, /set_speaker_amplifier\(false\)/);
  assert.match(audio, /board_audio_peek_levels/);
  assert.match(audio, /old tones instead of replaying them/);
  assert.match(audio, /command_listening/);
  assert.match(commandRecognizer, /\[MULTINET\] inference=start/);
  assert.match(commandRecognizer, /\[MULTINET\] inference=stop/);
  assert.match(provisioning, /probe_configured_gateway/);
  assert.match(provisioning, /esp_netif_str_to_ip4/);
  assert.match(provisioning, /routed private networks are valid/);
  assert.match(provisioning, /network-test/);
  assert.match(provisioning, /sameSubnet/);
  assert.match(provisioning, /example\.com/);
  assert.match(provisioning, /gatewayHttpsReachable/);
  assert.match(provisioning, /candidate sync skipped while the Gateway was unreachable/);
  assert.match(logger, /MAX_BYTES=5242880/);
  assert.match(logger, /stty -F/);
  assert.match(logger, /SHELL_FRAMER/);
  assert.match(shellFramer, /cut -c/);
  assert.match(logger, /ARCHIVES=7/);
  assert.match(logger, /snowball-board\.sh/);
  assert.match(debugTool, /usage: snowball-esp32-debug status\|probe/);
  assert.match(debugTool, /report \[minimum-cycles\]/);
  assert.match(debugTool, /audio-self-test/);
  assert.match(debugTool, /probe-network/);
  assert.match(debugTool, /publicKey.*<redacted>/);
  assert.match(debugTool, /collector.*stopped|stopped.*collector/);
  assert.match(debugTool, /socat/);
  assert.match(traceReport, /complete_cycles/);
  assert.match(traceReport, /media_connected/);
  assert.match(traceReport, /Guru Meditation/);
  assert.match(browserController, /findVoicePickerBackButton/);
  assert.match(browserController, /Back to chat\|채팅으로 돌아가기/);
  assert.match(browserController, /blocking dialog without a supported close control/);
  assert.match(browserController, /frontendLoginShellVisible/);
  assert.match(browserController, /recoverFrontendLoginShell/);
  assert.match(browserController, /anonymous Voice/);
  assert.match(browserController, /picker and leaves/);
  assert.match(boardHelper, /multiple Snowball USB boards found/);
  assert.doesNotMatch(logger, /BOARD_SERIAL=['"][0-9A-Fa-f:]{12,}/);
  assert.match(flasher, /build-cache-safe2/);
  assert.match(flasher, /python -m esptool/);
  assert.match(flasher, /approved command-tail firmware is missing/);
  assert.match(flasher, /SNOWBALL_FLASH_TRANSPORT/);
  assert.match(flasher, /jtag_flash_verified/);
  assert.match(flasher, /0x310000/);
  assert.match(flasher, /refusing this recovery path/);
  assert.match(flasher, /0x9000/);
  assert.match(flasher, /serial_boot_check/);
  assert.match(flasher, /USBJTAGSerialReset/);
  assert.match(flasher, /port\.dtr = True/);
  assert.match(flasher, /USB-JTAG may briefly re-enumerate/);
  assert.match(flasher, /EN-only reset/);
  assert.match(flasher, /def hard_reset/);
  assert.match(flasher, /application_boot_verified/);
  assert.match(flasher, /board_remained_in_download_mode/);
  assert.match(flasher, /hold BOOT, tap RESET/);
  assert.match(flasher, /SNOWBALL_FLASHER_SOURCE/);
  assert.match(flasher, /Installed ESP32 flasher is stale/);
  assert.match(flasher, /sha256sum/);
  assert.match(scenarios, /one-phrase English\s+controls/);
  assert.match(scenarios, /20 minutes/);
});

test("Production deployment is opt-in and keeps an automatic rollback target", async () => {
  const deploy = await import("node:fs/promises").then((fs) =>
    fs.readFile(new URL("../tools/deploy-candidate.sh", import.meta.url), "utf8"),
  );
  assert.match(deploy, /SNOWBALL_DEPLOY_APPROVAL/);
  assert.match(deploy, /\[ "\$APPROVAL" = YES \]/);
  assert.match(deploy, /docker rename "\$CONTAINER" "\$ROLLBACK"/);
  assert.match(deploy, /docker rename "\$ROLLBACK" "\$CONTAINER"/);
  assert.match(deploy, /api\/auth\/status/);
  assert.match(deploy, /api\/health/);
  assert.match(deploy, /127\.0\.0\.1:3100\/status/);
});
