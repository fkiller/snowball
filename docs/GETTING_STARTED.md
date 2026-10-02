# Buy, build, and run Snowball

This guide covers **Snowball-Voice-Gate**, the LAN Gateway and web console,
and **Snowball-Voice**, the ESP32 speaker client. This is a developer preview:
expect a terminal, local certificate installation, and manual ChatGPT login.
Read [privacy](PRIVACY.md) and [firmware limitations](../SECURITY.md) before buying.
No hardware, paid account, or third-party service is supplied by this repository.

## 1. Choose and buy the hardware

| Item | What to obtain | Notes |
| --- | --- | --- |
| Gateway host | ARM64 or AMD64 Linux machine with Docker support; Windows/macOS can control a LAN Linux VM/host | Physically validated on an 8 GB ARM64 FriendlyWrt/OpenWrt router. A spare host is preferable to changing your primary router. See the [platform guide](PLATFORMS.md) for each installation path. |
| RAM/storage | 8 GB RAM and at least 20 GB free build/storage space recommended | Planning allowance, not a measured minimum. Chromium uses a 1 GB shared-memory mount; ESP-IDF's container is large. No swap requirement. |
| Speaker | Waveshare **ESP32-S3-AUDIO-Board**, ESP32-S3R8, 16 MB flash, 8 MB PSRAM | Generic ESP32/S3 boards and other codecs are not drop-in compatible. ESP32-S3-AUDIO-Board-EN is a vendor variant; it is not independently accepted here. |
| Audio/power | Vendor-compatible speaker and USB-C power arrangement | Check the seller's package contents and the board manual. Confirm the speaker is connected; do not buy an arbitrary impedance/power replacement. Battery and microSD are unnecessary for Snowball. |
| Cable | USB data cable, USB-C at the board end | A charge-only cable cannot flash or provision. |
| Setup computer | Windows, macOS, or Linux with desktop Chrome/Edge | Its browser must support Web Serial and temporarily own the USB cable. Phone Safari cannot perform USB pairing. |
| Network | Private IPv4 LAN and **2.4 GHz Wi-Fi** | ESP32 joins 2.4 GHz; Gateway may be wired. Disable client isolation or allow the documented LAN ports between the devices. Never forward them from WAN. |
| Service access | Your own ChatGPT account with Voice available | Availability and limits depend on the service/account. An OpenAI API key is not used by this integration. |

Use the [manufacturer's board page](https://www.waveshare.com/esp32-s3-audio-board.htm)
to buy from Waveshare or a local authorized seller. Confirm the exact model,
memory, included speaker/cable, and current price with the seller.
The [official board manual](https://docs.waveshare.com/ESP32-S3-AUDIO-Board)
and [schematic/resources](https://docs.waveshare.com/ESP32-S3-AUDIO-Board/Resources-And-Documents)
are the reference for connectors, power, and the BOOT/RESET buttons.

You can test the Gateway's Web Client without purchasing the ESP32 first.
Internet is required for ChatGPT, login providers, and dependency downloads;
LAN-only does not mean offline inference.

## 2. Install the Gateway tools

The supported build/run path is a Linux Docker host. The container includes
Node, Go, nginx, Chromium, and audio services: native Node/Go are only needed
for contributors running checks outside Docker.

On Ubuntu/Debian install Git, then install Docker Engine and its Compose plugin
using [Docker's official distribution-specific instructions](https://docs.docker.com/engine/install/).
Use a distribution/version supported by Docker; do not apply Ubuntu package
commands to OpenWrt. Verify:

For Ubuntu 24.04, first install prerequisites with
`sudo apt update` and `sudo apt install git ca-certificates curl iproute2`.
The Docker guide then walks through adding its signing key/package repository
and installing `docker-ce docker-ce-cli containerd.io docker-buildx-plugin
docker-compose-plugin`. Verify the daemon is started before the commands below.
For Debian use Docker's Debian guide instead. No global Node/Go installation
is required to compile the Gateway container.

```sh
git --version
docker version
docker compose version
```

If Docker reports permission denied, use the administrator's supported Docker
access method. Membership of the Docker group grants host-level authority.

For Windows contributors install [Git for Windows](https://git-scm.com/downloads/win),
[Node.js 22 LTS](https://nodejs.org/en/download), and [Go 1.26.8 or newer](https://go.dev/dl/).
Docker Desktop with the Linux engine/WSL2 can build images, but Windows Docker
Desktop runtime networking is not the accepted Gateway deployment path. Run
the Gateway on the Linux host and use Windows for compilation/USB setup.

### Existing OpenWrt installations

Install OpenWrt's Docker package through your distribution's package manager
only after checking storage, kernel/cgroup support, and the router firewall.
Use the existing daemon; do not stop unrelated services to install Snowball.
The project's previously deployed router uses a dedicated socket:

```sh
export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock
docker version
```

Keep that socket and existing data paths for existing installations. Optional
existing-daemon boot integration and migration are documented in
[OpenWrt operations](OPENWRT.md). Migration changes the operating environment
and requires its own tested maintenance procedure; it is not a first-install step.

## 3. Download the source and configure the LAN

```sh
git clone https://github.com/fkiller/snowball.git
cd snowball
```

Before cloning a prerelease by tag, check [Releases](https://github.com/fkiller/snowball/releases)
and choose an existing tag. `0.4.0-alpha.1` is the prepared release version;
it is not an assurance that a release/image has been published.

Edit `compose.yaml`:

- Set `SNOWBALL_LAN_IP` to a **private IPv4 address actually assigned to the
  Linux host**. `192.168.1.1` is an example/router default, not autodiscovery.
- Reserve the host address in your router's DHCP configuration.
- Keep internal RTP/controller ports on loopback. The only LAN ports are
  TCP 8088 (CA bootstrap), TCP 8443 (HTTPS), and UDP 49000 (WebRTC).
- Keep the container/volume identifiers. Do not change them to match display
  product names: persistent credentials depend on `snowball-voice` and `/data`.
- Use an isolated spare host for a first install. Do not run a second host-network
  instance against the same ports or existing production volume.

Check the configuration, build from source, and start **only for a fresh install**:

```sh
docker compose config --quiet
docker build --network host -t snowball-voice:0.4.0-alpha.1 .
docker compose up -d
docker compose ps
docker inspect --format '{{.State.Health.Status}}' snowball-voice
```

Allow several minutes for first startup. A healthy container is necessary,
but does not mean ChatGPT is signed in or a speaker is paired. If the container
exits, inspect local logs without sharing them publicly:

```sh
docker logs --tail 100 snowball-voice
```

For an existing installation, stop here and use [upgrade/rollback](OPENWRT.md#upgrade-and-rollback).
Do not use `docker compose down -v`: it deletes persistent credentials/state.

## 4. Set up HTTPS and sign in

Substitute your configured address and ports for the examples below.

1. From a trusted setup computer visit `http://192.168.1.1:8088` and download
   the generated local CA. Install it as a trusted root only for your own Gateway.
   CA bootstrap must occur on a trusted LAN; do not accept an unknown host's CA.
2. Windows: import into **Trusted Root Certification Authorities** for the
   intended user/computer. macOS: import in Keychain Access and explicitly trust.
   Linux: use your distribution/browser's trusted-CA procedure. Restart the browser.
3. iPhone/iPad clients additionally enable full trust under **Settings → General
   → About → Certificate Trust Settings** after installing the profile.
4. Read the one-time initial setup code locally with `docker logs snowball-voice`.
   Visit `https://192.168.1.1:8443` and create the Snowball administrator password.
   Do not share the setup code, password, or container logs.
5. Open **Browser Console**, sign in to your own ChatGPT account, and complete
   any manual challenge. Snowball admin login and ChatGPT login are separate.
   Neither signing in nor opening Browser Console starts Voice.
6. Return to the Voice console and start a Web Client conversation. Approve
   microphone permission, verify the authoritative Voice state, speak, hear a
   response, then stop. The UI must return to idle.

If HTTPS is untrusted, fix the CA installation rather than disabling browser
security. Web Serial and microphone features need a secure context.

## 5. Install ESP-IDF and compile Snowball-Voice

Use **ESP-IDF 5.5.5**. The dependency lock pins components and speech models;
do not edit it to bypass a build failure. Arduino firmware is not this project.
Keep the board disconnected while building; building never requires flashing.

### Windows: native ESP-IDF

Install [Espressif Installation Manager](https://dl.espressif.com/dl/eim/) and
select **ESP-IDF 5.5.5** with ESP32-S3 tools. The offline 5.5.5 bundle is an
alternative when downloads fail. Open the installed **ESP-IDF PowerShell**
environment so its Python and toolchain are on PATH.

```powershell
git clone https://github.com/fkiller/snowball.git
cd snowball
idf.py --version
python -m esptool version
$env:PYTHONUTF8='1'
$env:PYTHONIOENCODING='utf-8'
cd firmware/esp32-s3-audio
idf.py -D SDKCONFIG=build-release/sdkconfig -D SDKCONFIG_DEFAULTS=sdkconfig.defaults -B build-release build
cd ../..
```

The repository selects `esp32s3`; a separate `set-target` command is not needed.
The UTF-8 variables prevent the vendor speech-model report from failing on
Windows consoles configured with a legacy code page.
Build output must include `bootloader/bootloader.bin`,
`partition_table/partition-table.bin`, `snowball_speaker.bin`, and
`srmodels/srmodels.bin` under `build-release`. The selected WakeNet phrase is
**Hi ESP**; `ChatGPT` is not the shipped wake model.

### Linux/macOS: Docker firmware build

This needs Docker but not a native ESP-IDF installation:

```sh
docker pull espressif/idf:v5.5.5
docker run --rm --network host \
  -v "$PWD/firmware/esp32-s3-audio:/project" -w /project \
  espressif/idf:v5.5.5 \
  bash -lc 'source /opt/esp/idf/export.sh && idf.py -D SDKCONFIG=/project/build-release/sdkconfig -D SDKCONFIG_DEFAULTS=/project/sdkconfig.defaults -B build-release build'
```

For native Linux/macOS serial flashing, install the same IDF outside this
repository with the [official setup guide](https://docs.espressif.com/projects/esp-idf/en/v5.5.5/esp32s3/get-started/):

```sh
mkdir -p "$HOME/esp"
cd "$HOME/esp"
git clone --recursive --branch v5.5.5 https://github.com/espressif/esp-idf.git
cd esp-idf
./install.sh esp32s3
. ./export.sh
```

Install the OS prerequisites listed by Espressif before `install.sh`, then
return to the Snowball repository. Re-export this environment in each new shell.

### Using a published firmware archive instead of compiling

Once a firmware prerelease actually exists, download its ESP32-S3 `.tar.gz`
and `.sha256` from Releases. Verify the checksum using the commands in the
[platform guide](PLATFORMS.md#common-release-installation). Check out the
matching source tag so the protected flash helpers and instructions match.
Keep the native IDF Python/esptool environment for USB programming.

From the matching repository root, create `firmware/esp32-s3-audio/build-release`
(`New-Item -ItemType Directory -Force` in PowerShell; `mkdir -p` on Linux/macOS),
then extract the archive's contents, removing its enclosing directory:

```sh
tar -xzf PATH_TO_DOWNLOADED_FIRMWARE.tar.gz --strip-components=1 -C firmware/esp32-s3-audio/build-release
```

Windows includes `tar`; replace the download path for your computer. Confirm
the four binaries and `flash_args` exist directly under `build-release` before
continuing. Downloading an archive does not authorize erasing NVS or flashing
a factory backup. A source preview may have no published firmware asset yet.

## 6. Flash without erasing identity

Before the **first write**, back up the vendor/factory flash privately. A full
flash backup may contain credentials; never add it to Git or attach it to an issue.
Do not enable Secure Boot/flash-encryption release mode or burn eFuses during
this development setup.

Connect the board directly to the setup computer with a USB **data** cable.
Close serial monitors and the Admin Web Serial connection before flashing.
Find the device port in Windows Device Manager or `/dev/ttyACM*` (Linux) /
`/dev/cu.usbmodem*` (macOS). Multiple attached boards require explicit selection.

From the activated IDF environment, a first backup can be made with:

```sh
python -m esptool --chip esp32s3 --port PORT read_flash 0 ALL /PRIVATE/PATH/factory-backup.bin
```

Replace `PORT` and the private absolute output path; on Windows use e.g.
`--port COM3` and a private Windows path. Check the backup exists and record its
SHA-256 locally. If the ROM handshake fails, follow the vendor's BOOT/RESET
instructions; release BOOT before the final reset.

Windows, from the repository root in ESP-IDF PowerShell:

```powershell
./tools/flash-esp32-windows.ps1 -Port COM3 -BuildDir firmware/esp32-s3-audio/build-release
```

Linux/macOS, from the repository root in the activated IDF environment:

```sh
SNOWBALL_SERIAL_PORT=/dev/ttyACM0 ./tools/flash-esp32-local.sh firmware/esp32-s3-audio/build-release
```

The protected helpers write only bootloader `0x0`, partition table `0x8000`,
application `0x10000`, and models `0x310000`. **NVS at `0x9000` must not be
written or erased.** Do not use `erase-flash`, a merged full-flash image, or
unreviewed `idf.py flash` as an upgrade shortcut.

Observe boot with `idf.py -B build-release -p PORT monitor` from the firmware
directory. Require `Snowball speaker ready` / `WakeNet detector ready`, and no
panic/reset loop. Correct flash hashes alone do not prove the board booted.
Close the monitor before pairing. The router-specific flash helper in
`tools/flash-esp32.sh` additionally manages its USB collector and JTAG recovery;
it is not a generic workstation helper.

## 7. Pair over USB, then use Wi-Fi

1. Keep the board connected to the **computer running desktop Chrome/Edge**.
   A Windows browser cannot pair a board still plugged into the router.
2. Visit the trusted HTTPS Gateway `/admin`, sign in as Snowball administrator,
   and choose **Connect USB device**. Select the Espressif port.
3. Enter the 2.4 GHz Wi-Fi SSID/password and the Gateway's reachable private
   address. Admin provisions a short-lived, one-use enrollment token and CA pin.
   Credentials travel over USB, not through Docker USB passthrough.
4. Require configured Wi-Fi, pinned Gateway reachability, no pending enrollment,
   and an **active** registry record matching the board's identity.
5. Close Web Serial and disconnect USB data if desired; keep the board powered.

See [pairing acceptance](PAIRING_ACCEPTANCE.md) for all four gates. A chime,
saved Wi-Fi, or WebRTC connection alone does not prove successful pairing.

## 8. Verify the first conversation

- Say **Hi ESP**, wait for command resolution, then speak. The initial sync may
  need time after a new board boots. Only the built-in control catalog is
  available before the authenticated Gateway candidate sync completes.
- Confirm browser Voice is active, microphone frames reach the Gateway, and
  ChatGPT's response plays through the physical speaker.
- End with **Hi ESP** while conversing, and verify both media and browser Voice
  return to idle. Repeat three times and check for a spontaneous wake after ending.
- `Hi ESP Resume` and bounded English voice/project command tails are secondary
  cases; see [wake commands](WAKE_COMMANDS.md). Codex local projects require an
  unavailable paired desktop adapter and return an explicit capability error.
- AEC is disabled in the stable development profile. Echo/barge-in quality is
  a known limitation, separate from the proven 10m30s transport continuity test.

Use [test scenarios](TEST_SCENARIOS.md) and [full-duplex evidence](FULL_DUPLEX_STABILITY.md)
for measured thresholds. Physical three-cycle/acoustic acceptance and a 30-minute
soak remain release checklist items; do not infer a pass from the build.

## Troubleshooting and upgrades

| Symptom | First check |
| --- | --- |
| Cannot open HTTPS / Web Serial unavailable | Correct host IP, trusted CA, desktop Chrome/Edge, secure context |
| Board absent | Data cable, OS device/driver, BOOT/RESET, one process owning the port |
| Wi-Fi connected but pairing incomplete | 2.4 GHz network, routing/client isolation, CA pin, registry activation |
| Chime but no Voice | Admin pairing gates and browser login/state; a tone is only local recognition |
| ChatGPT login/CAPTCHA | Protected Browser Console; human recovery is required |
| No sound / echo | Vendor speaker connection, audio self-test, stable AEC-disabled limitation |
| Firmware build fails | Exact IDF 5.5.5 environment, locked components, free storage, build log |
| Container build/run fails | Docker daemon/context, architecture, free storage, LAN address assigned to host |

Back up `/data` privately before upgrades. Gateway replacement preserves the
same volume; board updates preserve NVS. Use the approved candidate deployment
and rollback procedure, not an automatic production restart. Never ship your
personal state or factory backup with a release.

## Contributor verification

Install native Node 22 and Go 1.26.8+ only if running host checks. From the root:

```sh
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
(cd gateway && go test -race ./... && go vet ./...)
docker build --network host -t snowball-voice:test .
SNOWBALL_QA_IMAGE=snowball-voice:test ./tools/run-qa-browser-smoke.sh
```

On Windows, `npm test` works from PowerShell. `go vet ./...` and
`go test ./devproto ./emulator` work in `gateway`; the full permission and
`/tmp` suite is authoritative on Linux with the race detector.

Follow [release planning](RELEASE_PLAN.md) for tags, checksums, licenses, SBOMs,
image publication, and remaining hardware/product acceptance.
