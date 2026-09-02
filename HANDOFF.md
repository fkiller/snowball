# Agent Handoff

## Status

**Handoff state: READY**

This handoff was explicitly requested by the user on 2026-09-01 EDT; it was
not triggered by the quota threshold. CodexBar reported 48% of the five-hour
window used (52% remaining) and 44% of the weekly window used (56% remaining).

## Current Objective

Snowball should support much faster voice iteration without requiring a person
to repeatedly operate the physical speaker. The intended future loop is a
machine-local paired-speaker emulator that uses real enrollment, signed device
messages, PCMA WebRTC, the actual LAN Gateway, repeatable synthesized audio,
downlink capture, and local transcription/oracles.

The immediate repository task was narrower:

1. record the emulator architecture as a plan, not implement it;
2. prevent logs, builds, identities, keys, captures, and runtime state from
   entering Git or Docker build contexts;
3. preserve and document a separate pre-existing uncommitted ESP32-S3 firmware
   candidate;
4. prepare this handoff without starting new implementation work.

## Current Task and State

The repository is intentionally dirty. There are three uncommitted work groups:

1. **Firmware candidate:** ghost-wake suppression, immediate media startup,
   pre-Voice preservation, and `Hi ESP` session termination.
2. **Hygiene/design:** ignore rules, `.gitattributes`, README link, and emulator
   plan.
3. **Process/tools:** expanded `AGENTS.md` and two Windows ESP32 helpers.

Do not combine these blindly. The firmware candidate still needs acceptance;
the hygiene/design group is coherent but was not staged because handoff was
requested immediately.

### Repository hygiene and emulator plan

- Root trace logs, root `build-*`, `eim_config.toml`, captures, PCAPs,
  certificates/keys, emulator state, `/data`, and runtime state are Git-ignored.
- The same sensitive/generated inputs are excluded from Docker build context.
- `git add --dry-run .` excludes logs, generated ESP-IDF builds, and
  `eim_config.toml`.
- No `.log` exists in reachable Git history and no tracked file exceeded 5 MiB
  when checked during cleanup.
- `docs/DEVICE_EMULATOR_PLAN.md` contains the full emulator plan. No emulator
  implementation exists.
- `.gitattributes` sets LF for Linux/container/firmware source and CRLF for
  PowerShell. Existing files still show expected line-ending conversion
  warnings. No mass renormalization was done because it would obscure diffs.

### Firmware candidate

The dirty source implements these candidate behaviors:

- Voice states: `IDLE`, `COMMAND_WINDOW`, `CONNECTING`, `CONVERSATION`, `ENDING`.
- Bare `Hi ESP` starts default new-chat media immediately while MultiNet runs
  its 1.8-second optional tail parser in parallel.
- A later tail result updates the attempt-scoped command before Gateway dispatch.
- Microphone capture starts with the media session, but uplink waits for the
  Gateway's signed terminal receipt confirming authoritative browser Voice.
- Up to 200 40-ms G.711A frames (8 seconds) are retained in PSRAM and replayed
  at real-time cadence after browser readiness.
- During conversation, a channel-verified `Hi ESP` transitions to `ENDING`,
  stops media locally, and returns to `IDLE` after cleanup.
- Partial WakeNet detections are rejected during conversation and immediately
  post-session; the post-session policy is generation-scoped.
- Re-arm includes a 3-second cooldown, AFE reset, fresh-frame drain, and quiet
  energy gate.
- Feedback explicitly enables/disables the amplifier and stale feedback
  generations are not replayed.
- MultiNet logs inference start/stop reason, frames, and elapsed time.
- DTLS close is handled through peer `DISCONNECTED` state in the media task,
  which remains cleanup owner.

An ignored build appears to correspond to the immediate-start candidate:

```text
firmware/esp32-s3-audio/build-minis-immediate/snowball_speaker.bin
size: 2232272 bytes
timestamp: 2026-09-01 11:08:33 EDT
sha256: 15e5c685cdf4567136651cd6f66ca486f7e8ceab858cae596a6cfcbd1a5c7762
```

This is not proof of a reproducible build or physical acceptance.

### Existing physical evidence

The ignored `trace-hi-esp-immediate-3.log` is later than current source
timestamps. A read-only scan found 14 `media_connected`, 14 `media_ended`, 21
`command_executed` string occurrences, 6 conversation partial-wake rejections,
12 post-session partial-wake rejections, and no Guru/cache-disabled fault.

Recent attempts reached `CONNECTING -> CONVERSATION`, enabled uplink, accepted a
verified in-conversation `Hi ESP` as end, reached `media_ended`, and returned to
`IDLE`. This is evidence, not formal acceptance: the Windows trace lacks a
router collector-session marker, and does not prove `Resume`, all command tails,
meaningful input transcription, or audible downlink quality.

## Exact Next Action

Before changing source, verify the candidate with one fresh physical collector
session:

1. build current source into a new named ESP-IDF build directory;
2. flash only bootloader, partition table, app, and SR models—never NVS at
   `0x9000`;
3. capture three consecutive `Hi ESP` start -> media connected -> command
   executed -> `Hi ESP` end -> media ended cycles, then `Hi ESP Resume`;
4. run `tools/esp32-voice-trace-report.sh LOG SESSION 3` on the router log;
5. review the known issues below before committing the firmware candidate.

If hardware is unavailable, do not claim it passed. Review and commit only the
hygiene/design group separately, then ask whether to implement the emulator or
wait for physical acceptance.

## Architecture and Decisions

### Emulator boundary

Build a **paired Snowball protocol/media emulator**, not cycle-accurate ESP32-S3
emulation. Desktop coverage can include P-256 identity/enrollment, replay
counters, signed commands/SDP, PCMA DTLS-SRTP, browser/Gateway state, audio
timing, local STT, and network faults.

WakeNet/MultiNet acoustic accuracy, ES7210/ES8311, I2S, PSRAM/cache safety,
echo, and real speaker/microphone behavior remain a short physical-board gate.
Timeline annotations may trigger recognized wake/command events but must not
claim to run ESP-SR.

### Shared protocol code

If emulator implementation is explicitly authorized, first extract Go wire
types, proof canonicalization, signing helpers, and replay rules from
`gateway/devices.go` into a package shared by Gateway and emulator. Add fixed
Go/C P-256 vectors. Do not handwrite a second canonical signing format.

### Test tiers

- **Fast:** temporary state, fake controller, deterministic answer audio, no
  production profile.
- **Real:** explicitly selected LAN Gateway and persistent Chromium; never
  restart or replace production implicitly.
- Local transcription should use a pinned CPU tooling image. The host had no
  ffmpeg, Whisper/faster-whisper, PyTorch, or Kokoro in the inspected Python.

### Security boundary

- Stay LAN-only; no wildcard bind, cloud relay, STUN, or TURN.
- Do not add an authentication bypass for emulator pairing.
- Use protected Admin enrollment and normal one-time P-256 proof.
- Keep keys, counters, sessions, generated audio, transcripts, logs, and
  captures outside tracked source.
- WebRTC alone is never success; browser state and bound device peer must agree.
- Never write/flash NVS `0x9000`, which contains Wi-Fi/pairing/device identity.

## Files Changed

| File | Purpose | State |
|---|---|---|
| `.dockerignore` | Exclude sensitive/generated inputs from Docker context. | Modified, unstaged |
| `.gitignore` | Exclude logs, builds, keys, captures, and runtime state. | Modified, unstaged |
| `.gitattributes` | Stable line endings and binary media types. | Untracked |
| `README.md` | Link emulator plan. | Modified, unstaged |
| `docs/DEVICE_EMULATOR_PLAN.md` | Emulator architecture/delivery/acceptance plan. | Untracked |
| `AGENTS.md` | Quota and cross-agent rules plus Snowball rules. | Modified, unstaged; preserve/review separately |
| `firmware/esp32-s3-audio/main/app_main.c` | Command update, uplink gate, local end callback. | Modified candidate |
| `firmware/esp32-s3-audio/main/board_audio.c` | Amplifier and feedback-generation lifecycle. | Modified candidate |
| `firmware/esp32-s3-audio/main/command_recognizer.[ch]` | MultiNet stop API/timing logs. | Modified candidate |
| `firmware/esp32-s3-audio/main/media_session.[ch]` | 8-second buffer, paced replay, gated uplink, DTLS cleanup. | Modified candidate |
| `firmware/esp32-s3-audio/main/speech.[ch]` | Voice states, parallel tail, end wake, ghost-wake gates. | Modified candidate |
| `tests/rendered-html.test.mjs` | Source assertions for firmware safety markers. | Modified, unstaged |
| `tools/esp32-serial-trace-windows.ps1` | Reconnecting Windows serial logger. | Untracked; raw output ignored |
| `tools/flash-esp32-windows.ps1` | Fixed-range non-NVS Windows flasher. | Untracked; machine-specific Python path |
| `HANDOFF.md` | This handoff. | Handoff-only commit if verification succeeds |

Ignored artifacts remain intentionally: root `trace-hi-esp-*.log`, root
`build-minis-ghost/`, firmware `build-minis-*`, and `eim_config.toml`. Do not
delete them during takeover.

## Tests and Verification

### Passed during handoff

```text
npm run lint                                      PASS
$env:WRANGLER_LOG_PATH='.wrangler/wrangler.log'
npx vinext build                                  PASS
node --test tests/*.test.mjs                      PASS: 15/15
cd gateway; go vet ./...                          PASS
git diff --check                                  PASS (conversion warnings only)
```

### Environment-dependent failures

- `npm test` failed before build because `package.json` uses POSIX
  `WRANGLER_LOG_PATH=...`; the equivalent PowerShell build and all Node tests
  passed.
- `go test -race ./...` did not run because CGO is disabled.
- `go test ./...` ran but failed on Windows assumptions: three private-mode
  assertions observed `0666`, and the peer-close test uses absent absolute
  `/tmp/...` paths. No functional Gateway assertion failure was observed before
  these platform failures. Re-run in Linux.

### Not run

- `npm ci --ignore-scripts`
- `npm audit --omit=dev --audit-level=high`
- Linux `go test -race ./...`
- fresh ESP-IDF rebuild
- `docker build --network host -t snowball-voice:test .`
- fresh physical acceptance

Docker Desktop's Linux daemon did not become ready within 60 seconds. WSL has
no usable distro; Ubuntu install failed with `HCS_E_HYPERV_NOT_INSTALLED`. Do not
enable Hyper-V or reboot merely for handoff verification without coordination.

## Known Problems / Open Questions

1. **Eight-second buffer saturates.** Traces show `preserved_frames=200` while
   browser completion is roughly 9-12 seconds. Immediate speech can be dropped;
   the emulator plan includes 8/9-second boundary cases.
2. **Command update race.** If media connects before the 1.8-second tail
   resolves, Gateway may queue default `new_chat` before the tail updates the
   command. Define/test synchronization.
3. **Duplicate trace semantics.** Bare wake records default `new_chat` and the
   later timeout result, producing two `command_resolved` entries per attempt.
4. **No active AEC.** AEC/SE/NS/VAD are disabled. Partial echo detections are
   rejected, but verified false positives and acoustic quality need hardware.
5. **Low memory watermark.** Later trace entries reached about 6 KiB
   `internalMin`; no crash occurred, but repeated-session stability needs review.
6. **Formal acceptance incomplete.** Existing logs do not prove `Resume`, all
   tails, input transcription, or audible downlink.
7. **Windows flasher portability.** It hardcodes
   `C:\Espressif\tools\python\v5.5.5\venv\Scripts\python.exe`.
8. **Windows npm portability.** build/test/start scripts use POSIX env syntax.
9. **No emulator implementation authorization.** User asked for plan/cleanup,
   not construction. Ask before starting substantial implementation.

## Failed or Abandoned Approaches

- Do not make cycle-accurate ESP32/QEMU the primary loop; ESP-SR and Waveshare
  peripherals are not faithfully represented.
- Direct `npm test` on PowerShell fails at POSIX env assignment; use Linux or
  the documented PowerShell equivalent.
- Race testing cannot run with the current CGO-disabled Windows toolchain.
- POSIX trace report cannot formally select raw Windows logs without collector
  session markers.
- Official upstream CodexBar has Linux/macOS CLI assets but no Windows CLI.
  Docker startup timed out and WSL Ubuntu failed because Hyper-V is unavailable.
  The installed native Windows solution below works.

## CodexBar Installation

The user explicitly requested installation after the initial quota command was
missing. Win-CodexBar 0.54.0 was installed through Winget package
`Finesssee.Win-CodexBar` (repository `nesszer/Win-CodexBar`; installer hash was
verified by Winget). A user-local wrapper exists at:

```text
C:\Users\wondo\AppData\Roaming\npm\codexbar.cmd
```

It forwards `codexbar --format json` to
`codexbar-cli.exe usage --provider codex`. This is machine state, not a repo
file, and the exact `AGENTS.md` command now works in PowerShell.

## Constraints

- Follow `AGENTS.md`; verify this file against Git before changes.
- Preserve all user changes; do not reset, clean, or delete ignored evidence.
- Keep Snowball LAN-only and API routes deny-by-default.
- Do not expose admin sessions/device secrets to browser JavaScript or logs.
- Do not restart/replace production router container without explicit approval.
- Browser state is authoritative; WebRTC alone is not Voice success.
- Do not flash NVS, enable Secure Boot/flash encryption, or burn eFuses.
- Stop the USB collector before Web Serial/flashing; serial has one owner.
- Treat ignored traces as potentially identifying.

## Useful Commands

```powershell
# Takeover inspection
git status --short --branch
git log -10 --oneline --decorate
git diff --stat
git diff
Get-Content -Raw AGENTS.md
Get-Content -Raw HANDOFF.md

# Quota
codexbar --format json

# Windows verification that passed
npm run lint
$env:WRANGLER_LOG_PATH='.wrangler/wrangler.log'
npx vinext build
node --test tests/*.test.mjs
Push-Location gateway
go vet ./...
Pop-Location

# Inspect ignored evidence without adding it
git status --short --ignored
Get-FileHash -Algorithm SHA256 firmware/esp32-s3-audio/build-minis-immediate/snowball_speaker.bin

# Logger example; choose actual port/path
powershell -NoProfile -File tools/esp32-serial-trace-windows.ps1 `
  -Port COMX -LogPath trace-next.log -DurationSeconds 900
```

```bash
# Required Linux verification before relevant implementation commit
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
cd gateway && go test -race ./... && go vet ./...
docker build --network host -t snowball-voice:test .

# New named firmware build
docker run --rm \
  -v "$PWD/firmware/esp32-s3-audio:/project" \
  -w /project espressif/idf:v5.5.5 \
  bash -lc 'source /opt/esp/idf/export.sh && \
    idf.py -D SDKCONFIG=/project/build-handoff-verify/sdkconfig \
      -D SDKCONFIG_DEFAULTS=/project/sdkconfig.defaults \
      -B build-handoff-verify build'

tools/esp32-voice-trace-report.sh /path/to/router-trace.log SESSION 3
```

## Git State

**Branch:** `codex/fix-post-bye-ghost-wake`

No upstream is configured. `origin/codex/snowball-minis-handoff` points to the
pre-handoff implementation commit, but the local branch name differs.

**Latest relevant implementation commit before handoff:**

```text
2837784 Prepare Snowball IoT handoff
```

Only earlier commit: `ec08ba5 Bootstrap Snowball`.

**Staged before handoff commit:** none. **Stashes:** none.

**Uncommitted tracked modifications:**

```text
.dockerignore
.gitignore
AGENTS.md
README.md
firmware/esp32-s3-audio/main/app_main.c
firmware/esp32-s3-audio/main/board_audio.c
firmware/esp32-s3-audio/main/command_recognizer.c
firmware/esp32-s3-audio/main/command_recognizer.h
firmware/esp32-s3-audio/main/media_session.c
firmware/esp32-s3-audio/main/media_session.h
firmware/esp32-s3-audio/main/speech.c
firmware/esp32-s3-audio/main/speech.h
tests/rendered-html.test.mjs
```

**Untracked non-ignored files before handoff commit:**

```text
.gitattributes
docs/DEVICE_EMULATOR_PLAN.md
tools/esp32-serial-trace-windows.ps1
tools/flash-esp32-windows.ps1
```

`HANDOFF.md` is committed separately if final verification succeeds. All other
changes intentionally remain unstaged/uncommitted for review and safe splitting.

## Recommended Continuation

1. Read `AGENTS.md` and this file, then verify status/history/diffs.
2. Perform the Exact Next Action without changing source first.
3. Review buffer saturation, command race, duplicate trace, and memory watermark.
4. Run full Linux verification and commit firmware only if physically accepted.
5. Review/commit hygiene/design separately; review `AGENTS.md` and tools apart.
6. Ask before implementing emulator; if authorized, start with shared protocol
   package and Go/C golden vectors in `docs/DEVICE_EMULATOR_PLAN.md`.

## Handoff Metadata

**Previous agent:** Codex

**Reason:** Explicit user-requested cross-agent handoff

**Five-hour quota:** 48% used / 52% remaining

**Weekly quota:** 44% used / 56% remaining

**Handoff state:** READY
