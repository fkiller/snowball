# Speaker pairing deployment and acceptance

Pairing is a prerequisite for every ESP32-to-Gateway control or media test. A
wake sound, a USB connection, saved Wi-Fi settings, or a WebRTC connection is
not evidence that pairing succeeded.

## Release state versus runtime state

The pairing implementation can exist in a source tree or candidate image while
the production Gateway still runs an older release. Do not describe pairing as
available until the candidate image has been deployed and its Admin page has
passed the checks below.

Runtime observations such as a board hardware ID, public-key fingerprint,
Wi-Fi status, or device-registry contents belong in local diagnostics and the
authenticated Admin UI. Do not copy device-specific identifiers into this
version-controlled document.

## One supported setup path

1. Deploy a tested Snowball image containing Admin authentication, the device
   registry, and the Web Serial pairing page. Preserve the existing `/data`
   volume and Chromium profile.
2. Open `https://<gateway-lan-ip>:<https-port>/admin` (the default router
   installation is `https://192.168.1.1:8443/admin`), complete initial Snowball
   administrator setup, and trust the Gateway's local CA on the setup computer.
3. Connect the board by USB-C to any Windows, macOS, or Linux computer. Open the
   Gateway's HTTPS Admin page in desktop Chrome or Edge, click **Connect USB
   device**, select the Espressif port, and enter the speaker's Wi-Fi settings.
4. Admin creates a short-lived, one-use enrollment token. The browser sends the
   Wi-Fi secret, private Gateway address, CA pin, and token directly to the
   board over Web Serial. Docker never receives USB access or the Wi-Fi secret.
5. The board joins Wi-Fi, verifies the pinned Gateway CA, and proves possession
   of its generated P-256 private key. The Gateway activates the matching
   device record and consumes the token.

The USB cable may be removed after acceptance. No microSD card is required in
the speaker.

The computer running Chrome/Edge must temporarily own the USB cable. A browser
on Windows cannot use Web Serial to reach a board that is still plugged into
the router; the router USB connection is only the development/log collector.
Snowball intentionally does not add a second host-CLI or Bluetooth setup path.

## Controlled deployment and rollback

The deployed Snowball candidate container contains the Admin enrollment
endpoints. After the test image passes
all repository gates, deployment requires one explicitly approved maintenance
restart:

1. Keep the old image and container configuration as the rollback target and
   preserve the existing persistent `/data` volume.
2. Stop the old container, start the tested image with the same volume and LAN
   bindings, and check health, Admin authentication, protected Browser Console,
   Chromium readiness, and ChatGPT login state.
3. If any check fails, stop the new container and restart the preserved previous
   image with the same unchanged volume. Do not attempt pairing.
4. Only after the deployment checks pass, perform Web Serial pairing.

Before deployment, visiting the production Web UI will show no pairing control;
that is an undeployed release, not evidence that the source implementation is
working in production.

This combines authentication, pairing, and the minimal `Hi ESP` media adapter
in one restart; there is no reason to disrupt the persistent ChatGPT session
twice.

## Required acceptance gates

Admin may show **Pairing complete** only when the first three pairing gates
pass. The first runtime request must then pass the fourth gate before any Voice
action can execute:

1. **Board gate:** the USB status is `configured:true`,
   `wifiConnected:true`, `gatewayReachable:true`, and
   `enrollmentPending:false`. Wi-Fi association alone is not a pairing
   success; the board must reach the pinned Gateway endpoint from that LAN.
2. **Registry gate:** authenticated `GET /api/devices` contains an `active`
   record whose hardware ID and public-key fingerprint exactly match the USB
   identity.
3. **Proof gate:** the active record exists only after the Gateway verifies the
   board's P-256 enrollment signature over the one-time token and nonce.
4. **Runtime activation gate:** the first signed media offer with a fresh boot
   nonce/counter is accepted, while a replay and an unknown, pending, or
   revoked identity are rejected. This is automatic on the first `Hi ESP`; it
   is not another user setup step.

Voice can execute only after all four gates pass. The first voice acceptance
case is deliberately just **Hi ESP**: it creates the authenticated PCMA
full-duplex media session, make authoritative browser state report
`voiceActive:true`, carry microphone audio to ChatGPT, and return ChatGPT audio
through the board speaker. Command variants remain secondary until this
baseline passes.

## Failure visibility

- Admin displays board status and the Gateway registry side by side and refuses
  to turn a board-only completion into a success message.
- The board keeps a bounded 32-event RAM trace available over USB.
- The router's optional USB trace collector keeps bounded, rotated logs on the
  router SD card; it must be stopped while Web Serial owns the port.
- A recognition chime means only local wake detection. It never means pairing,
  command execution, media connection, or ChatGPT Voice succeeded.
