# Snowball Architecture

Snowball is a single-host, LAN-only gateway between a client browser and a persistent ChatGPT Voice session. The design favors an inspectable real browser, explicit network bindings, and recoverable failure modes over invisible browser automation.

## System context

```mermaid
flowchart LR
    User((User))
    Client["Snowball PWA<br/>iOS / desktop browser"]
    Router["Home router<br/>Snowball container"]
    ChatGPT["ChatGPT web app"]
    Push["Browser push service"]

    User --> Client
    Client <-->|"HTTPS 8443<br/>WebRTC UDP 49000"| Router
    User -->|"sign-in / CAPTCHA<br/>through recovery console"| Router
    Router <-->|"HTTPS + voice media<br/>outbound only"| ChatGPT
    Router -->|"recovery alert<br/>outbound only"| Push
    Push --> Client
```

There is no inbound WAN service. Snowball listens on one configured private IPv4 address and relies on the router's existing firewall for the LAN trust boundary. Outbound access is still required for ChatGPT, identity-provider login, and Web Push delivery.

## Container topology

```mermaid
flowchart TB
    subgraph Host["ARM64 router host"]
        Dockerd["Dedicated dockerd<br/>no bridge / NAT / iptables"]

        subgraph Container["Snowball container · host network · non-root"]
            Nginx["nginx<br/>LAN :8088 / :8443"]
            Web["Snowball PWA<br/>127.0.0.1:3000"]
            Gateway["Go gateway<br/>API 127.0.0.1:8080<br/>WebRTC LAN UDP :49000"]
            Controller["Browser controller<br/>127.0.0.1:3100"]
            CDP["Chromium CDP<br/>127.0.0.1:9222"]
            Chromium["Headed Chromium<br/>persistent ChatGPT profile"]
            Display["Xvfb :99"]
            VNC["x11vnc :5900<br/>websockify/noVNC :6080"]
            Pulse["PulseAudio<br/>virtual input + output"]
            GstUp["GStreamer uplink<br/>RTP :49001"]
            GstDown["GStreamer downlink<br/>RTP :49002"]
            Supervisor["Supervisor<br/>process recovery"]
            Data[("/data volume<br/>profile · certs · push state")]

            Nginx --> Web
            Nginx --> Gateway
            Nginx --> VNC
            Gateway --> Controller
            Controller --> CDP --> Chromium
            Chromium --> Display --> VNC
            Gateway <--> GstUp
            Gateway <--> GstDown
            GstUp --> Pulse
            Pulse --> Chromium
            Chromium --> Pulse
            Pulse --> GstDown
            Chromium <--> Data
            Gateway <--> Data
            Nginx <--> Data
            Supervisor -.-> Web
            Supervisor -.-> Gateway
            Supervisor -.-> Controller
            Supervisor -.-> Chromium
            Supervisor -.-> VNC
            Supervisor -.-> Pulse
            Supervisor -.-> GstUp
            Supervisor -.-> GstDown
            Supervisor -.-> Nginx
        end

        Dockerd --> Container
    end
```

Supervisor owns every long-running process. If Chromium is closed, it is restarted with the same `/data/chromium` profile and the controller reconnects to its loopback-only DevTools endpoint.

## Voice media path

Snowball keeps control traffic on HTTPS and carries audio as Opus over WebRTC. The two media directions are intentionally symmetric but use separate PulseAudio virtual devices.

```mermaid
flowchart LR
    Mic["Client microphone"]
    Speaker["Client speaker"]
    Pion["Pion WebRTC gateway<br/>UDP 49000"]
    Up["GStreamer<br/>Opus decode"]
    MicSink["chatgpt_mic_sink"]
    MicSource["chatgpt_mic_source<br/>remapped input"]
    Browser["Chromium / ChatGPT"]
    OutputSink["chatgpt_output_sink"]
    Down["GStreamer<br/>Opus encode"]

    Mic -->|"WebRTC Opus"| Pion
    Pion -->|"RTP UDP 49001<br/>loopback"| Up
    Up --> MicSink --> MicSource -->|"getUserMedia"| Browser

    Browser -->|"page audio"| OutputSink
    OutputSink -->|"monitor source"| Down
    Down -->|"RTP UDP 49002<br/>loopback"| Pion
    Pion -->|"WebRTC Opus"| Speaker
```

Chromium policy pre-authorizes audio capture only for `https://chatgpt.com` and its HTTPS subdomains. PulseAudio exposes the WebRTC uplink as a named remapped source because Chromium does not reliably surface a monitor device as a microphone.

## Authentication and voice activation

Authentication is a human recovery workflow, not a voice-start side effect.

Snowball administrator authentication is an additional, separate boundary. On first start the Gateway writes a one-time setup code to protected state and the container log. The user exchanges it for an administrator password; the password is stored as a salted adaptive hash. Voice, WebRTC, Push, settings, and Browser Console access then require a Secure/HttpOnly/SameSite session. State-changing API calls also require a session-bound CSRF token and a same-origin request.

```mermaid
sequenceDiagram
    actor User
    participant PWA as Snowball PWA
    participant GW as Go gateway
    participant BC as Browser controller
    participant Chrome as Chromium

    User->>PWA: Open Browser Console

    PWA->>GW: Snowball administrator session
    GW-->>PWA: Secure session + CSRF token
    PWA-->>User: Live Chromium desktop via noVNC
    User->>Chrome: Sign in / solve challenge
    BC->>Chrome: Inspect cookies and page state
    BC-->>GW: authenticated=true, voiceActive=false
    GW-->>PWA: ready

    User->>PWA: Tap voice control
    PWA->>GW: GET /api/status
    alt not authenticated
        GW-->>PWA: needs_login
        PWA-->>User: Open Browser Console
    else authenticated and ready
        PWA->>GW: POST /api/webrtc/offer
        GW-->>PWA: WebRTC answer
        PWA->>GW: POST /api/voice/start
        GW->>BC: start
        BC->>Chrome: Activate ChatGPT Voice UI
        BC-->>GW: voiceActive=true
        GW-->>PWA: Voice is live
    end
```

The controller considers the session authenticated only when a ChatGPT session cookie exists. A visible voice-shaped control on a signed-out page is not sufficient. Consequently, a voice-start request from a fresh profile is rejected instead of being queued across login.

## Human recovery and notifications

```mermaid
sequenceDiagram
    participant BC as Browser controller
    participant GW as Go gateway
    participant Push as Web Push service
    actor User
    participant Console as noVNC console
    participant Chrome as Chromium

    BC->>Chrome: Inspect page state
    Chrome-->>BC: login / CAPTCHA / recovery required
    BC-->>GW: needs_login or needs_human
    GW->>Push: Send recovery notification
    Push-->>User: Alert
    User->>Console: Open notification target
    Console-->>User: Live Chromium window
    User->>Chrome: Complete recovery
    BC-->>GW: ready
```

Push subscriptions and VAPID keys live under `/data/state`. Notifications are advisory: the recovery console remains reachable directly on the LAN even if the external push service is unavailable.

## Network and trust boundaries

| Binding | Process | Exposure |
| --- | --- | --- |
| `${SNOWBALL_LAN_IP}:8088/tcp` | nginx | LAN CA download and redirect |
| `${SNOWBALL_LAN_IP}:8443/tcp` | nginx | LAN HTTPS UI, API, and console |
| `${SNOWBALL_LAN_IP}:49000/udp` | Go gateway | LAN WebRTC ICE/media |
| `127.0.0.1:3000/tcp` | web app | Container/host loopback only |
| `127.0.0.1:3100/tcp` | browser controller | Loopback only |
| `127.0.0.1:5900/tcp` | x11vnc | Loopback only |
| `127.0.0.1:6080/tcp` | websockify/noVNC | Loopback only; proxied by nginx |
| `127.0.0.1:8080/tcp` | Go gateway API | Loopback only; proxied by nginx |
| `127.0.0.1:9222/tcp` | Chromium CDP | Loopback only |
| `127.0.0.1:49001/udp` | GStreamer uplink | Loopback RTP |
| `127.0.0.1:49002/udp` | GStreamer downlink | Loopback RTP |

Defense-in-depth controls include:

- explicit private-IPv4 validation before the WebRTC listener starts
- explicit LAN and loopback binds despite host networking
- a dedicated Docker daemon with bridge, forwarding, masquerade, and Docker iptables management disabled
- read-only root filesystem, tmpfs runtime directories, dropped Linux capabilities, `no-new-privileges`, and a non-root UID
- TLS from a device-local CA for secure-context browser features
- a loopback-only CDP endpoint and a Chromium audio-capture allowlist
- persistent secrets and browser credentials only in the Docker volume
- first-run administrator bootstrap, rate-limited login, in-memory sessions, and an authenticated noVNC proxy
- origin and CSRF checks for every state-changing control request
- schema-driven validation and atomic private writes for editable settings

The headed Chromium process currently requires `--no-sandbox` in this container. The container restrictions and router firewall are therefore essential boundaries rather than optional hardening.

## Persistence and lifecycle

```mermaid
flowchart TD
    Build["Immutable ARM64 image"] --> Start["Container start"]
    Volume[("Persistent /data volume")] --> Start
    Start --> Certs{"Certificates exist?"}
    Certs -->|No| Generate["Generate local CA + server cert"]
    Certs -->|Yes| Services["Start supervised services"]
    Generate --> Services
    Services --> Profile["Reuse Chromium profile"]
    Services --> PushState["Reuse VAPID keys + subscriptions"]
    Failure{"Process exits?"} -->|Yes| Restart["Supervisor restarts process"]
    Restart --> Services
```

The image is replaceable; `/data` is not. Upgrades must preserve the existing named volume to retain the ChatGPT session, certificates, and notification registrations.

## Design constraints and trade-offs

- **LAN first:** no STUN or TURN keeps the v1 network model small, but prevents remote use without a separate trusted network overlay.
- **Real browser:** a headed browser makes OAuth, CAPTCHA, and UI recovery possible, but introduces an unstable dependency on ChatGPT's DOM and browser requirements.
- **Local TLS:** a private CA enables microphone and service-worker features without a public hostname, at the cost of per-device certificate enrollment.
- **Host networking:** it avoids Docker bridge and router-policy interference, but demands strict explicit binding and makes the host firewall part of the design.
- **Persistent web session:** it avoids storing an OpenAI password in application code, but makes the Chromium volume highly sensitive.
- **Separate local authentication:** ChatGPT cookies stay origin-bound inside Chromium and never become Gateway credentials. This adds one Snowball login but prevents any LAN client from inheriting browser-control authority.
- **Wake input is client-specific:** command parsing and browser execution live in the Gateway, while an ESP32 must detect the wake phrase locally. Browser speech services are not silently used because they can leave the LAN.

See [Wake commands and project turn mode](docs/WAKE_COMMANDS.md) and [Client discovery and pairing security](docs/CLIENT_DISCOVERY_SECURITY.md) for the command contract, capability boundaries, first-power user stories, and future device trust model.

## Source map

- `gateway/main.go` — WebRTC, RTP forwarding, browser control proxy, status, and Web Push
- `services/browser-controller.mjs` — ChatGPT state inspection and voice UI control
- `app/voice-console.tsx` — client microphone, WebRTC lifecycle, status, console, and notifications
- `container/pulse/default.pa` — virtual microphone and output devices
- `container/start-gst-*.sh` — RTP/audio conversion pipelines
- `container/nginx.conf.template` — the LAN-only HTTPS edge
- `container/supervisord.conf` — process lifecycle and recovery
- `runtime/daemon.json` — isolated Docker daemon behavior
