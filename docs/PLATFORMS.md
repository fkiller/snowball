# Snowball-Voice-Gate platform releases, installation, and execution

All packages use the same tagged source, protocol 1, and `snowball-voice`
runtime identity. Packages are developer previews. The coordinated release contains all platform bundles, two Linux image
archives, firmware, checksums, SBOMs, and corresponding source.

| Platform | Release package | Execution path | Acceptance status |
| --- | --- | --- | --- |
| Linux ARM64 | `snowball-voice-gate-VERSION-linux-arm64.tar.gz` | Docker Engine directly on LAN host | Physical router evidence exists for 0.3.10; new-tag CI/image checks required |
| Linux AMD64 | `snowball-voice-gate-VERSION-linux-amd64.tar.gz` | Docker Engine directly on LAN host | Build/smoke gate per architecture; physical acceptance separately recorded |
| OpenWrt ARM64 | Linux ARM64 package | Existing host Docker; optional procd integration | Preserve current daemon/state; boot/migration is a manual gate |
| Windows x64/ARM64 | `snowball-voice-gate-VERSION-windows.zip` | PowerShell/SSH launcher → Linux VM or separate LAN Linux host | Input validation implemented; VM/media acceptance still manual |
| macOS Intel/Apple Silicon | `snowball-voice-gate-VERSION-macos.tar.gz` | shell/SSH launcher → Linux VM or separate LAN Linux host | Input validation implemented; VM/media acceptance still manual |

Windows/macOS packages contain source, launchers, configuration, and guides.
Chromium/audio run in the Linux container. Linux images are built for ARM64
and AMD64. A Windows/macOS bundle is not a Windows/macOS native Chromium/audio
server executable. This is intentional: Snowball binds a specific LAN address
and relies on Linux host networking; desktop Docker networking is a distinct
boundary and must not be advertised as accepted without dedicated validation.
Docker Desktop's [documented host-network limitations](https://docs.docker.com/engine/network/drivers/host/#limitations)
include inability to bind the desktop host's interface addresses directly.

## Common release installation

1. Download the matching platform bundle and its `.sha256` from
   [GitHub Releases](https://github.com/fkiller/snowball/releases).
2. Verify SHA-256: Linux `sha256sum FILE`; macOS `shasum -a 256 FILE`;
   Windows `Get-FileHash FILE -Algorithm SHA256`. Compare to the release checksum.
3. Extract to a private working directory. Follow the platform section below.
   Use `download` below to fetch, checksum, and load the matching prebuilt
   Linux image. Alternatively use `build` to compile the included Dockerfile.
4. Complete [CA trust and login](GETTING_STARTED.md#4-set-up-https-and-sign-in),
   then [ESP32 build/flash/pairing](GETTING_STARTED.md#5-install-esp-idf-and-compile-snowball-voice).

## Linux ARM64 and AMD64

Install Git, Docker Engine/Compose using the official instructions linked in
[getting started](GETTING_STARTED.md#2-install-the-gateway-tools), and `iproute2`, Bash, curl, and gzip.
Check `uname -m`: `aarch64`/`arm64` selects ARM64; `x86_64` selects AMD64.
Docker builds natively for that host. Other architectures are unsupported.

Inside the extracted package/source checkout:

```sh
docker version
docker compose version
ip -4 address
sh tools/gate-start.sh download
SNOWBALL_LAN_IP=192.168.1.20 sh tools/gate-start.sh install
sh tools/gate-start.sh status
```

Use your actual private host IP, not the example. Installation refuses a
non-private/unassigned address or an existing container. Open
`http://192.168.1.20:8088` for CA setup, then `https://192.168.1.20:8443`.
The normal Docker restart policy handles service recovery; an approved OS
reboot acceptance test remains part of host onboarding.

Existing installations use upgrade/rollback, never fresh-install commands.
To run host development checks install Node 22 and Go 1.27.1+; see CONTRIBUTING.

## Windows x64 and ARM64

Install Git for Windows, desktop Chrome/Edge, and Windows OpenSSH Client
(Settings → Optional features → OpenSSH Client). Verify `git --version` and
`ssh -V` in PowerShell. Node/Go are optional contributor tools. ESP32 native
compilation uses the pinned IDF/EIM setup in the main guide.

Choose either a separate ARM64/AMD64 Linux machine on the LAN or a Linux VM.
For Windows Pro/Enterprise/Education, a Hyper-V Ubuntu VM with an **External
virtual switch** can receive its own LAN IPv4; see
[Microsoft's virtual-switch guide](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/get-started/create-a-virtual-switch-for-hyper-v-virtual-machines).
Follow the hypervisor's current requirements and preserve your host network
configuration. Windows Home or systems without a usable bridged VM can use a
separate LAN Linux host; WSL's default NAT is not an accepted LAN deployment.
Do not introduce a WAN tunnel, wildcard bind, or cloud relay to compensate.

Install Ubuntu for the VM's actual architecture (x64 or ARM64), enable its SSH
server (`sudo apt install openssh-server` on Ubuntu), verify its private address
with `ip -4 address`, then install Git/Docker/Compose **inside Linux**. Confirm
the setup computer and ESP32 subnet can reach this address.

For a Windows Hyper-V VM: enable Hyper-V in **Turn Windows features on or off**,
reboot if requested, open Hyper-V Manager, create an External switch on the LAN
adapter, and create a Generation 2 VM. Allocate about 8 GB RAM and at least
30 GB disk, attach the appropriate [Ubuntu installer ISO](https://ubuntu.com/download),
connect the External switch, and complete Ubuntu installation. Set firmware
Secure Boot to the hypervisor's Ubuntu-compatible certificate template if
needed. Inside Ubuntu reserve/record the guest's private IP and verify SSH
from Windows before installing Docker. Do not assume ARM64 Windows has the
same hypervisor availability as x64; a separate LAN Linux host is the fallback.

In the VM's Linux terminal:

```sh
git clone --branch v0.4.0-alpha.1 https://github.com/fkiller/snowball.git ~/snowball
```

Use the matching tag `v0.4.0-alpha.1` on the Linux host.
In Windows PowerShell, from the extracted Windows bundle:

```powershell
ssh user@192.168.1.20
# Exit after checking the VM host key and Docker access.
./tools/gate-windows.ps1 -GateHost user@192.168.1.20 -RemotePath /home/user/snowball -Action download
./tools/gate-windows.ps1 -GateHost user@192.168.1.20 -RemotePath /home/user/snowball -Action install -LanIp 192.168.1.20
./tools/gate-windows.ps1 -GateHost user@192.168.1.20 -RemotePath /home/user/snowball -Action status
```

Replace `user`, path, and IP. The account needs supported Docker access inside
Linux. The launcher uses the tagged source already installed there. The explicit
`download` action checks the release image checksum and loads it inside Linux. Open the Linux VM's
HTTPS URL in Windows Chrome/Edge. The ESP32 USB cable stays on Windows for
Web Serial pairing; it does not need VM USB passthrough.

## macOS Intel and Apple Silicon

Install Git with Apple Command Line Tools (`xcode-select --install`) and desktop
Chrome/Edge. macOS includes SSH. For ESP32 native compilation, install the
prerequisites and IDF 5.5.5 per Espressif's guide linked from getting started.

Use a separate LAN Linux host or a Linux VM with its own reachable LAN address.
For example [UTM's network documentation](https://docs.getutm.app/settings-qemu/devices/network/network/)
describes bridged networking for a QEMU VM. Use Ubuntu ARM64 on Apple Silicon
and Ubuntu AMD64 on Intel; verify that your chosen hypervisor/network mode
actually exposes a guest LAN address. Shared/NAT mode alone does not meet this
project's LAN listener/ICE requirements. Bridging over some Wi-Fi adapters is
limited; use Ethernet or a separate LAN Linux host if necessary.

For a UTM VM: install UTM from its official site, create a Linux VM using the
appropriate Ubuntu ISO, allocate about 8 GB RAM and 30 GB disk, and configure
the bridged network mode supported by that VM backend. Finish Ubuntu's
installation, install its SSH server, reserve/record the guest LAN IPv4,
and test SSH from macOS. Verify the actual VM backend supports bridging before
using it; merely selecting an accelerated VM does not establish LAN reachability.

Inside Linux, install SSH, Git, Docker Engine/Compose, and clone the matching
source tag into `/home/user/snowball`. From the macOS bundle:

```sh
ssh user@192.168.1.20
# Exit after host-key and Docker-access checks.
sh tools/gate-macos.sh user@192.168.1.20 /home/user/snowball download
sh tools/gate-macos.sh user@192.168.1.20 /home/user/snowball install 192.168.1.20
sh tools/gate-macos.sh user@192.168.1.20 /home/user/snowball status
```

Open the guest's HTTPS URL on macOS, trust its CA in Keychain Access, sign in,
and pair the ESP32 with Web Serial on macOS. The VM does not need USB access.
Keep the VM powered while using Voice. VM autostart/power recovery depends on
the hypervisor and is a manual platform acceptance gate.

## OpenWrt ARM64

Use your distribution's supported Docker/kernel/storage setup, not Ubuntu
package commands. Existing installations keep their current socket:

```sh
export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock
docker version
```

Follow [OpenWrt operations](OPENWRT.md) for optional boot integration and
approved migration. Do not restart the production container or change its
daemon while merely building/checking a release. A fresh OpenWrt host may use
its existing Docker daemon; verify its firewall/LAN binds and data storage.

## Platform release acceptance

Every advertised platform needs: clean install, correct LAN-only binds,
administrator setup, protected console, ChatGPT login, Web Client media,
ESP32 pairing/media, stop/recovery, reboot/autostart, and upgrade/rollback.
CI building a package does not prove those user workflows were physically
tested. Record passes and unexecuted cases in RELEASE_READINESS.md.

## Downloaded image and source verification

Each Linux architecture has a separate `*-image.tar.gz` plus `.sha256`
and `*-image-id.txt`. The download helper runs `sha256sum -c` before
`docker load`; loading does not start or replace a container. For offline
installation, download both files, verify locally, and run
`docker load -i IMAGE.tar.gz`, then follow `install` above. Never run a
fresh-install helper over an existing production container.

`*-corresponding-source.tar.gz` contains exact Debian source descriptors,
upstream archives, Debian build scripts, Node/noVNC sources, Go dependencies,
project source and license notices. If split, verify each `.part-NN` checksum
and concatenate in numerical order: `cat NAME.tar.gz.part-* > NAME.tar.gz`.
Extract and follow its `REBUILD.md`. SHA-256 and exact commit are also in
`release-manifest.json`; keep source alongside redistributed images.
