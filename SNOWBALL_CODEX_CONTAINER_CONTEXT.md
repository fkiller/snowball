# Snowball Router — Codex 컨테이너 환경 컨텍스트

이 문서는 Snowball Router의 새 Codex 세션 시작 시 그대로 붙여 넣는 운영 컨텍스트다. 아래 사실과 규칙을 먼저 숙지하고, 작업 전에 현재 상태를 다시 확인하라. Snowball-minis로 개발을 옮길 때는 이 문서를 현재 환경으로 오해하지 말고 `docs/SNOWBALL_MINIS_HANDOFF.md`를 먼저 읽어라.

## 1. 장비와 운영 경계

- 장비: FriendlyWrt/OpenWrt 22.03.5, `rockchip/armv8`, `aarch64`, RAM 약 8GB, swap 없음.
- LAN 주소: `192.168.1.1`.
- Snowball은 LAN 전용이다. WAN listener, public bind, cloud relay, STUN/TURN을 추가하지 마라.
- 운영 Snowball Gateway 컨테이너와 그 `/data` 볼륨은 사용자가 명시적으로 배포를 요청하지 않는 한 중지·교체·삭제하지 마라.
- 민감한 인증 데이터, Chromium 세션, 인증서, Web Push 키, 비밀번호, 쿠키를 출력하거나 Git에 넣지 마라.

## 2. 현재 Codex 아키텍처

Native Codex 바이너리와 OpenWrt용 PID daemon shim은 제거했지만 복구용 백업에 보존되어 있다. 현재의 `/root/.local/bin/codex`는 native CLI가 아니라 Docker 진입 wrapper다.

```text
ChatGPT Codex Remote
  -> SSH root@SNOWBALL-ROUTER
  -> /root/.local/bin/codex
  -> host Docker CLI
  -> /var/run/snowball-voice-docker.sock
  -> codex-dev 컨테이너
  -> /opt/codex/bin/codex
```

현재 Codex 컨테이너:

- 이름: `codex-dev`
- 이미지: `codex-dev:0.144.4-idf5.5.5-full`
- Codex CLI: `0.144.4`
- Codex 실행 파일: 컨테이너 안 `/opt/codex/bin/codex`
- app-server Unix socket: `/root/.codex/app-server-control/app-server-control.sock`
- 컨테이너 안과 호스트의 프로젝트 절대 경로는 동일하다: `/root/snowball-voice`
- `/root/.codex`는 호스트 bind mount다. 세션·인증·메모리·설정은 이 경로에 보존된다.
- 컨테이너 restart policy: `unless-stopped`
- 메모리 제한: 6 GiB
- swap 확장 금지: `memory-swap=6 GiB`
- CPU 제한: 4 CPU
- PID 제한: 512
- 네트워크: `host`

`codex app-server daemon start|stop|restart|version`과 `codex remote-control start|stop`은 모두 이 wrapper를 통해 컨테이너 app-server를 대상으로 한다. native `/root/.codex/packages/standalone/current`를 복구하거나 직접 실행하려 하지 마라. `managedCodexPath`에 예전 경로가 표시되더라도 실제 실행 파일은 컨테이너의 `/opt/codex/bin/codex`다.

## 3. Docker 제어

Codex 컨테이너 안에는 Docker daemon이 없다. Docker-in-Docker를 만들지 마라. Docker CLI가 host의 전용 Docker daemon socket을 제어한다.

- 컨테이너 내부 표준 경로: `/var/run/docker.sock`
- 기존 호스트 경로도 호환용으로 mount되어 있다: `/var/run/snowball-voice-docker.sock`
- 실제 daemon socket: 호스트 `/var/run/snowball-voice-docker.sock`
- Docker data-root: `/mnt/sdcard/snowball-voice-docker`
- Docker daemon 설정: `/root/snowball-voice/runtime/daemon.json`
- 운영 Snowball 컨테이너: `snowball-voice`

컨테이너 안에서 일반적으로 다음을 사용하라.

```bash
docker ps
docker compose -f /root/snowball-voice/compose.yaml config
docker build --network host --pull=false -t snowball-voice:dev /root/snowball-voice
```

Docker bind mount의 source 경로는 daemon 호스트 기준이다. 따라서 host와 `codex-dev`에서 동일한 절대 경로를 유지해야 한다. `/workspace` 같은 컨테이너 전용 경로를 Docker service의 bind source로 사용하지 마라.

Docker socket 접근은 사실상 host 관리자 권한이다. 작업 범위를 Snowball 프로젝트와 필요한 테스트 이미지로 제한하고, `docker system prune`, 전체 image/volume 삭제, 운영 컨테이너 삭제를 임의로 실행하지 마라.

## 4. Snowball Gateway 작업 규칙

- 소스: `/root/snowball-voice`
- 운영 이미지 예: `snowball-voice:0.2.2`
- 운영 컨테이너: `snowball-voice`
- 운영 상태는 현재 healthy여야 한다.
- 변경 작업은 먼저 별도 image tag와 임시 volume으로 build/test하라.
- 사용자가 명시적으로 배포를 요청하기 전에는 `snowball-voice`를 restart/replace하지 마라.
- 현재 프로젝트에는 커밋되지 않은 사용자 변경이 많다. `git reset`, `git checkout --`, 강제 clean을 하지 마라.
- 기본 검증 기준은 프로젝트의 `AGENTS.md`를 따른다: frontend lint/test, Go test/vet, Docker build.

중요한 프로젝트 규칙:

- Snowball은 LAN-only다.
- 인증 API는 deny-by-default다.
- `/data`는 persistent Chromium 로그인 세션과 인증서가 있으므로 절대 커밋하거나 삭제하지 마라.
- Browser Console을 열거나 인증하는 동작이 Voice를 자동 시작하면 안 된다.

## 5. ESP32 작업

현재 보드 유형:

- 장치: `/dev/ttyACM0`
- USB VID/PID: `303a:1001`
- 칩: ESP32-S3, USB-Serial/JTAG
- 컨테이너에 `/dev/ttyACM0`가 passthrough되어 있다.
- ESP-IDF: `v5.5.5`, ARM64 이미지 `espressif/idf:v5.5.5`

보드 serial, MAC, hardware ID, 공개키 fingerprint는 `/etc/snowball-esp32-board.conf`와 인증된 런타임 진단에만 둔다. 이 문서나 Git에는 기록하지 마라.

Codex 컨테이너 안에서 build 산출물은 기존 `/project` build와 섞지 말고 다음 경로를 사용하라.

```bash
cd /root/snowball-voice/firmware/esp32-s3-audio
idf.py -B /mnt/sdcard/snowball-dev/codex-build/esp32-s3-audio build
```

기존 `firmware/esp32-s3-audio/build`는 과거 컨테이너의 `/project` 경로로 구성되어 있을 수 있다. 경로 오류가 나면 기존 build를 지우지 말고 새 build directory를 사용하라.

읽기 전용 장치 확인:

```bash
/opt/esp/python_env/idf5.5_py3.12_env/bin/python -m esptool --port /dev/ttyACM0 chip_id
```

주의: 호스트의 `snowball-esp32-debug` serial logger가 장치를 점유한다. Flash/deploy 전에는 호스트 maintenance 단계에서:

```bash
/etc/init.d/snowball-esp32-debug stop
# 필요한 idf.py flash 작업
/etc/init.d/snowball-esp32-debug start
```

를 수행하고, 중단되더라도 logger를 반드시 복구하라. `/root/snowball-voice/tools/flash-esp32.sh`는 logger 중지·보드 식별·flash·logger 복구를 처리하는 호스트용 helper다. Flash는 실제 firmware를 바꾸는 작업이므로 사용자가 명시적으로 요청한 경우에만 수행하라.

## 6. Remote/app-server 작업

Remote 접속은 SSH를 통해 `/root/.local/bin/codex`를 실행한다. wrapper가 `app-server proxy` 요청을 받으면:

1. `codex-dev`가 실행 중인지 확인한다.
2. 컨테이너 안 app-server가 없으면 자동 시작한다.
3. shared Unix socket을 확인한다.
4. 요청을 컨테이너 안의 Codex CLI로 전달한다.

확인 명령:

```bash
codex --version
codex app-server daemon version
codex remote-control start
```

정상 상태에서는 CLI/app-server가 `0.144.4`이고 socket이 존재해야 한다. Remote 연결이 안 되면 먼저 `codex-dev` 상태, socket, wrapper 로그를 확인하고 native daemon을 다시 설치하지 마라.

OpenAI workspace 정책이나 Remote Control 권한이 별도로 요구될 수 있으므로, 앱에서 Remote가 비활성화되어 있으면 workspace/RBAC와 ChatGPT desktop의 device discovery/control 설정도 확인하라.

## 7. 백업과 복구

마이그레이션 백업:

`/mnt/sdcard/codex-migration-backups/20260813T215146Z`

포함 내용:

- native Codex CLI와 standalone package
- wrapper 및 custom daemon service
- `/root/.codex` 세션/인증/설정 전체
- `/root/snowball-voice` 전체 working tree
- Git bundle와 unstaged diff
- Docker container/image/volume metadata
- SHA-256 검증 파일

제거된 native 파일은 백업의 `removed-native/` 아래에 이동되어 있으며 삭제되지 않았다. 복구가 필요하면 먼저 해당 백업의 SHA-256을 확인하고, 현재 컨테이너와의 충돌 여부를 검토한 뒤 사용자에게 확인을 받아라.

## 8. 향후 Codex 작업 방식

항상 다음 순서로 작업하라.

1. 현재 컨테이너·프로젝트·Git 상태를 읽는다.
2. 사용자 변경을 보존한다.
3. 운영 컨테이너를 건드리지 않고 별도 tag/temp volume에서 검증한다.
4. Docker socket을 사용할 때 source path가 host 기준인지 확인한다.
5. ESP32 장치를 사용할 때 serial logger 점유 여부를 확인한다.
6. build/test 결과와 실제 변경 파일을 요약한다.
7. 운영 restart/flash/삭제/cleanup은 명시적 승인을 받은 뒤에만 한다.

절대 하지 말 것:

- native Codex를 `/root/.codex/packages/standalone/current`에서 직접 되살리기
- OpenWrt PID-managed Codex daemon shim 재설치
- Docker daemon을 컨테이너 안에 또 띄우기
- `/root/.codex` 또는 Snowball `/data` 삭제
- 전체 Docker prune/volume 삭제
- 확인 없이 ESP32 flash
- 확인 없이 운영 `snowball-voice` 교체
