# Snowball-minis IoT handoff

Last updated: 2026-08-22

This document moves ESP32 build, flash, log analysis, and candidate deployment
off Snowball-router and onto the saved **Snowball-minis** development host.
Snowball-router remains the LAN Gateway and keeps the persistent `/data`
volume and Chromium login. Never copy that runtime state into this repository
or onto the development host.

## Current physical behavior

The latest user-observed run is the authoritative starting point:

1. Saying `Hi ESP` plays the 440/660 Hz two-note `wake_ready` sound.
2. About the bare-wake timeout later, the 880 Hz `new_chat` sound plays.
3. ChatGPT Voice starts and the conversation works.
4. Saying `Bye` ends ChatGPT Voice.
5. With no new speech, the same 440/660 Hz plus 880 Hz sequence plays again.

The tone mapping comes directly from `main/board_audio.c`. It proves that the
post-`Bye` symptom is not merely delayed Gateway audio: the ESP32 locally
entered the WakeNet path and resolved another bare wake. Whether that ghost
command also starts a second browser Voice session must be verified from the
Gateway's authoritative `voiceActive` state.

The current firmware already attempts to prevent this in `main/speech.c` with
a 3-second re-arm cooldown, an AFE buffer reset, 800 ms of fresh audio, and an
8-frame quiet gate. The physical result proves that this protection is not yet
sufficient. Do not tune those constants blindly. First capture the exact
post-session timeline and determine whether the trigger is stale AFE input,
speaker-to-microphone feedback, an old queued attempt, or an incorrect state
transition.

There is also an instrumentation discrepancy: the latest router collector
session contains continuing `audio_level` records but no `wake_detected`
record for the audible transition. A successful fix must make the physical
sound, the board trace, the signed Gateway receipt, and browser state agree.

## Sol plan versus Luna progress

| Sol work block | Luna result | Evidence / remaining gap |
| --- | --- | --- |
| Re-audit worktree, runtime, deployment, ESP logs | Completed | Runtime/container/log state was repeatedly inspected and the final Gateway image is recorded below. |
| Compare lifecycle, latency, and ghost-wake criteria to code/tests | Partially completed | Several lifecycle bugs were found, but the post-`Bye` ghost wake still reproduces physically. |
| Add instrumentation, regression tests, and operating docs | Mostly completed | Bounded USB traces, memory snapshots, a three-cycle report tool, browser/Gateway tests, and resource docs exist. The newest audible wake is missing from the collector trace, so observability is not accepted. |
| Run software QA, memory checks, WebRTC smoke, and idle soak | Completed for software | Node tests/lint/audit, Go race/vet, Docker build, isolated WebRTC smoke, direct controller start/stop, and a 20-minute idle soak passed on the last router run. These checks do not prove the physical speaker lifecycle. |
| Pass physical `Hi ESP` → Voice → `Bye` acceptance and command matrix | Not completed | One new-session start works, but `Bye` is followed by a ghost local wake. Three clean cycles, `Resume`, voice selection, and project commands remain unaccepted. |

The implementation and QA scaffolding progressed substantially, but the Sol
completion criterion was not met. Do not describe this milestone as complete
until the physical gate below passes.

## Current Gateway state

- Production container: `snowball-voice`
- Production image: `snowball-voice:final-20260821-live`
- Last verified state: healthy, ChatGPT browser authenticated, Voice idle
- Retained rollback container: `snowball-voice-rollback-20260821T212922Z`
- Gateway address: `192.168.1.1`
- HTTPS Admin: `https://192.168.1.1:8443/admin`

These names are operational evidence, not a request to restart production.
Every future production swap still requires explicit approval and must use
`tools/deploy-candidate.sh` so `/data` and a rollback container are preserved.

## Repository and sensitive-state boundary

Clone the private repository and start from its published default branch. The
repository intentionally excludes:

- ESP-IDF build directories and `sdkconfig` generated state;
- `/data`, Chromium profiles, cookies, certificates, passwords, and VAPID keys;
- board serials, MAC addresses, hardware IDs, and public-key fingerprints;
- router USB logs and local board-selection files.

Keep the local board selector in a mode-0600 runtime file. Do not add a specific
board identity to source, documentation, issues, or CI output.

## Snowball-minis readiness

From the cloned repository, run:

```sh
tools/snowball-minis-readiness.sh
```

Minimum working environment:

- Git with access to the private repository;
- Docker with at least 6 GiB available to a build container;
- at least 10 GiB free before downloading/building ESP-IDF artifacts;
- the pinned `espressif/idf:v5.5.5` image;
- direct USB access to the Waveshare ESP32-S3-AUDIO-Board;
- SSH access from Snowball-minis to `root@192.168.1.1` for read-only Gateway
  log correlation and, only after approval, controlled deployment.

Stop if free disk is below 5 GiB. Warn the user before building below 10 GiB.
Do not run a firmware build, web image build, and production restart at the
same time on a memory-constrained host.

## Board move and USB ownership

1. Connect the board directly to Snowball-minis.
2. Confirm the router collector has released/disconnected from the old port.
3. Allow exactly one local serial reader or flasher to own the new port.
4. Capture a read-only status/trace before changing flash.
5. Preserve NVS. Never write or erase the `0x9000` NVS range.

Linux commonly exposes the board as `/dev/ttyACM*`; macOS commonly uses
`/dev/cu.usbmodem*`. Do not assume a fixed device name. Resolve the single
Espressif USB-Serial/JTAG device at runtime and fail closed if more than one is
present.

## Reproducible firmware build

Use the pinned IDF version and a fresh ignored build directory. On a Linux
Docker host with direct USB support:

```sh
cd firmware/esp32-s3-audio
docker run --rm \
  -v "$PWD:/project" \
  -w /project \
  espressif/idf:v5.5.5 \
  bash -lc 'source /opt/esp/idf/export.sh >/dev/null && \
    idf.py -D IDF_TARGET=esp32s3 \
      -D SDKCONFIG=/project/build-minis/sdkconfig \
      -D SDKCONFIG_DEFAULTS=/project/sdkconfig.defaults \
      -B build-minis build'
```

On macOS, Docker Desktop does not generally provide raw USB passthrough. It is
fine to build in Docker, but flash with a pinned native ESP-IDF/esptool setup
or another explicit USB-capable environment. Do not invent a privileged relay
or expose the board over the Internet.

Before flashing, record:

- source commit and dirty/clean state;
- SHA-256 of application, bootloader, partition, and SR-model images;
- app-partition usage and static DIRAM from the build output;
- free disk and peak build memory.

The pre-transfer check on 2026-08-22 completed a fresh ignored build with
ESP-IDF 5.5.5 without flashing the board. The application was `0x220170`
bytes with `0xdfe90` bytes (29%) free in the smallest app partition. Static
DIRAM use was 173,631/341,760 bytes (50.8%). The build host stayed at about
2.7 GiB of its 6 GiB container limit and retained about 22 GiB free disk.
That run exposed a portability issue: the target had been inferred from a
local ignored `sdkconfig`. `CMakeLists.txt` and the command above now pin
`esp32s3`. Configuration of that explicit-target build succeeded; its
redundant full compile was stopped during handoff rather than consuming more
router time. Snowball-minis must therefore make the first complete build of
the published commit and produce compatible size/resource results before it
owns the first flash.

Flash only the explicit ranges already enforced by `tools/flash-esp32.sh`:

```text
0x000000  bootloader/bootloader.bin
0x008000  partition_table/partition-table.bin
0x010000  snowball_speaker.bin
0x310000  srmodels/srmodels.bin
```

The local Snowball-minis procedure must provide the same guarantees as the
router helper: one port owner, no NVS write, exact read-back or esptool hash
verification, a final application boot marker, and logger restoration even on
failure.

## First diagnostic run on Snowball-minis

Do not start by changing timing. Capture one controlled run:

1. Reboot into the current application and wait for `WakeNet detector ready`.
2. Start a timestamped serial log.
3. Record Gateway logs and `/status` timestamps from Snowball-router.
4. Say `Hi ESP` once, wait for Voice, speak one short request, and say `Bye`.
5. Remain silent for 60 seconds.
6. If the tones repeat, capture the board RAM `trace` immediately without
   resetting it.

Required questions for that trace:

- Was `speech_end_session()` called, and for which attempt generation?
- When did DTLS close and `media_ended` occur relative to `Bye`?
- Did WakeNet re-arm before or after all output playback stopped?
- Was the ghost state `WAKENET_DETECTED` or
  `WAKENET_CHANNEL_VERIFIED`?
- Did `board_audio_begin_wake()` increment a new generation?
- Did a second signed command envelope reach the Gateway?
- Did authoritative browser `voiceActive` become true again?

If the board emits the tones but the USB event is absent, fix serial/trace
observability before modifying recognition logic. A tone without a matching
attempt ID is not acceptable diagnostic evidence.

## Likely code focus

- `firmware/esp32-s3-audio/main/speech.c`: session end, AFE reset, fresh-audio
  and quiet re-arm gates, WakeNet state transition, attempt generation.
- `firmware/esp32-s3-audio/main/board_audio.c`: output/feedback lifecycle and
  speaker playback state exposed to the re-arm gate.
- `firmware/esp32-s3-audio/main/media_session.c`: DTLS close and terminal media
  ordering.
- `firmware/esp32-s3-audio/main/app_main.c`: stale attempt rejection and
  feedback/event ordering.
- `gateway/main.go`: authoritative browser state, device peer teardown, and
  cached signed-event receipts.

Prefer a state-machine fix with an explicit session/attempt generation over a
larger sleep. Add a regression test or deterministic host-side state test for
every discovered transition before flashing again.

## Physical completion gate

The fix is accepted only when a fresh firmware build produces all of the
following evidence:

1. Three consecutive `Hi ESP` → new ChatGPT Voice → request/response → `Bye`
   cycles complete without reset, panic, repeated feedback, delayed stale
   command, or spontaneous re-open.
2. After each `Bye`, 60 seconds of silence produces no wake or command tone and
   browser `voiceActive` remains false.
3. The board trace contains ordered `wake_detected`, `command_resolved`, TLS
   preflight, `media_connected`, terminal command receipt, and `media_ended`
   for each accepted attempt.
4. `tools/esp32-voice-trace-report.sh <log> <session> 3` exits zero for that
   single post-fix session.
5. No cache error, watchdog, unexpected reset, TLS memory-gate failure, or
   internal-largest-block regression occurs.
6. After the baseline passes, one `Hi ESP Resume` and the configured
   voice/project command matrix are tested separately.

All repository gates in `AGENTS.md` must pass before a Gateway candidate is
deployed. A green software suite never substitutes for this physical gate.

## Continuation prompt

Use the following prompt if automatic project transfer is unavailable:

```text
Snowball IoT 작업을 Snowball-minis에서 이어서 진행해.

1. private repo의 최신 기본 브랜치를 clone/fetch하고 AGENTS.md,
   SNOWBALL_PROJECT_CONTEXT.md, docs/SNOWBALL_MINIS_HANDOFF.md를 완전히 읽어.
2. tools/snowball-minis-readiness.sh를 실행하고 OS/arch, Docker/ESP-IDF,
   RAM, 디스크, USB 보드, Snowball-router SSH 접근을 확인해. 10GiB 미만이면
   빌드 전에 경고하고 5GiB 미만이면 중단해.
3. 현재 실물 상태는 Hi ESP → 440/660Hz wake 음 → 880Hz new_chat 음 →
   ChatGPT Voice 시작은 성공하지만, Bye 종료 후 아무 말도 하지 않아도
   같은 wake/new_chat 음이 다시 나는 ghost wake다.
4. 먼저 현재 펌웨어로 한 번 재현하면서 ESP32 serial/RAM trace, Gateway
   signed-event receipt, media/DTLS lifecycle, browser voiceActive 타임라인을
   같은 시계로 수집해. 로그에 없는 소리를 성공이나 원인으로 추측하지 마.
5. speech.c의 post-session re-arm, board_audio.c의 playback lifecycle,
   media_session.c/app_main.c의 attempt ordering을 점검하고, 단순 sleep 증가가
   아닌 명시적 session generation/state-machine 방식으로 최소 수정해.
6. ESP-IDF v5.5.5로 fresh ignored build를 만들고 메모리/파티션을 확인해.
   플래시는 사용자가 연결한 단일 보드에만 하고 NVS 0x9000은 절대 쓰지 마.
7. 세 번 연속 Hi ESP → Voice → Bye 후 각각 60초 무음에서 ghost wake가 없고,
   report 3이 같은 post-fix 세션으로 통과해야 완료다. 그 뒤 Resume와 나머지
   command matrix를 테스트해.
8. Gateway 변경은 별도 이미지/임시 상태로 모든 gate를 통과시킨 뒤에만,
   사용자 승인 후 rollback과 /data를 보존하는 deploy-candidate.sh로 배포해.
9. 진행 중 컨테이너 메모리와 디스크를 주기적으로 확인하고, 테스트 증거와
   실패 원인을 docs/SNOWBALL_MINIS_HANDOFF.md에 갱신해.
```
