FROM golang:1.27.1-bookworm AS gateway-builder
WORKDIR /src
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download
COPY gateway/ ./
RUN go mod vendor \
    && mkdir -p /out/third-party/go \
    && find vendor -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -iname 'COPYRIGHT*' \) -exec cp --parents '{}' /out/third-party/go/ \; \
    && cp /usr/local/go/LICENSE /out/third-party/go/GO-LICENSE
RUN GOMAXPROCS=1 CGO_ENABLED=0 GOOS=linux go build -p=1 -trimpath -ldflags="-s -w" -o /out/snowball-gateway .

FROM node:22-bookworm-slim AS web-builder

ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --ignore-scripts
COPY app ./app
COPY build ./build
COPY db ./db
COPY drizzle ./drizzle
COPY examples ./examples
COPY public ./public
COPY worker ./worker
COPY .openai ./.openai
COPY drizzle.config.ts eslint.config.mjs next-env.d.ts next.config.ts postcss.config.mjs tsconfig.json vite.config.ts ./
COPY tools/collect-node-notices.mjs ./tools/
RUN npm run build
RUN node tools/collect-node-notices.mjs

FROM node:22-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium \
    DISPLAY=:99 \
    PULSE_SERVER=unix:/tmp/pulse/native \
    XDG_RUNTIME_DIR=/tmp/runtime-pwuser \
    HOME=/data/home/pwuser \
    SNOWBALL_LAN_IP=192.168.1.1 \
    SNOWBALL_HTTP_PORT=8088 \
    SNOWBALL_HTTPS_PORT=8443 \
    SNOWBALL_ICE_PORT=49000 \
    SNOWBALL_UPLINK_RTP_PORT=49001 \
    SNOWBALL_DOWNLINK_RTP_PORT=49002 \
    SNOWBALL_DEVICE_UPLINK_RTP_PORT=49003 \
    SNOWBALL_DEVICE_DOWNLINK_RTP_PORT=49004

RUN apt-get update && apt-get upgrade -y && apt-get install -y --no-install-recommends \
      chromium \
      gettext-base \
      curl \
      gstreamer1.0-plugins-base \
      gstreamer1.0-plugins-good \
      gstreamer1.0-plugins-bad \
      gstreamer1.0-pulseaudio \
      gstreamer1.0-tools \
      nginx \
      novnc \
      openssl \
      pulseaudio \
      pulseaudio-utils \
      supervisor \
      websockify \
      x11-utils \
      x11vnc \
      xvfb \
    && useradd --create-home --uid 10001 --shell /usr/sbin/nologin pwuser \
    && rm -rf /var/lib/apt/lists/* /etc/nginx/sites-enabled/default

ARG NOVNC_VERSION=1.7.0
ARG NOVNC_SHA256=b1003a11b6e6e8d8f7f5e5586daae7f8ca651d8aee0aa155ff9ac841c48f52c6
RUN curl -fsSL "https://github.com/novnc/noVNC/archive/refs/tags/v${NOVNC_VERSION}.tar.gz" \
      -o /tmp/novnc.tar.gz \
    && echo "${NOVNC_SHA256}  /tmp/novnc.tar.gz" | sha256sum -c - \
    && mkdir -p /tmp/novnc-unpack \
    && tar -xzf /tmp/novnc.tar.gz -C /tmp/novnc-unpack \
    && rm -rf /usr/share/novnc \
    && mv "/tmp/novnc-unpack/noVNC-${NOVNC_VERSION}" /usr/share/novnc \
    && rm -rf /tmp/novnc.tar.gz /tmp/novnc-unpack

COPY container/chromium-policy.json /etc/chromium/policies/managed/snowball-voice.json

WORKDIR /opt/snowball
COPY LICENSE THIRD_PARTY_NOTICES.md ./
COPY LICENSES ./LICENSES
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && npm cache clean --force \
    && rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack /opt/yarn-v1.22.22 \
    && rm -f /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg
COPY services ./services
COPY container ./container
COPY --from=web-builder /src/dist ./dist
COPY --from=gateway-builder /out/snowball-gateway /usr/local/bin/snowball-gateway
COPY --from=gateway-builder /out/third-party ./third-party
COPY --from=web-builder /src/third-party/npm ./third-party/npm

RUN chmod +x container/*.sh /usr/local/bin/snowball-gateway \
    && mkdir -p /data/certs /data/chromium /data/state /data/home/pwuser \
    && chown -R pwuser:pwuser /data

VOLUME ["/data"]
EXPOSE 8088/tcp 8443/tcp 49000/udp

USER pwuser
HEALTHCHECK --interval=30s --timeout=5s --start-period=90s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/api/health >/dev/null || exit 1

ENTRYPOINT ["/usr/bin/supervisord", "-c", "/opt/snowball/container/supervisord.conf"]
