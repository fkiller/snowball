# Snowball repository rules

These rules apply to every change in this repository.

## Product boundary

- Snowball is LAN-only. Do not add a wildcard bind, inbound WAN listener, public deployment, STUN/TURN service, or cloud relay without explicit approval and a threat-model update.
- Preserve the `snowball-voice` container, volume, daemon, and state paths unless a tested migration is part of the change. The persistent Chromium session and credentials depend on them.
- Never commit `/data`, browser profiles, certificates, setup codes, passwords, session cookies, VAPID private keys, or generated runtime state.
- Do not restart or replace the production router container unless the user explicitly requests deployment. Build and test under a separate image tag and temporary state volume.

## Authentication and API safety

- Every API route is deny-by-default. Only health and authentication bootstrap/login/status routes may be public.
- Every authenticated state-changing route must use `g.protect(handler, true)`. Read-only sensitive routes use `g.protect(handler, false)`.
- Browser writes must go through the authenticated request helper so the CSRF header is present. Do not expose the session cookie to JavaScript.
- Keep the Browser Console behind nginx `auth_request`. ChatGPT authentication and Snowball administrator authentication are separate trust boundaries.
- Accept JSON only with strict unknown-field rejection and a bounded request body. Validate and normalize values before persistence or browser automation.
- Write sensitive state atomically with mode `0600`. Do not log passwords, cookies, CSRF tokens, or browser credentials. The one-time bootstrap code is the only setup secret allowed in logs and must be deleted after setup.

## Voice and browser automation

- Treat the browser controller as an unstable adapter. Prefer explicit capability errors over clicking a broad or ambiguous selector.
- The Gateway/browser status is authoritative for ChatGPT Voice state. A WebRTC connection alone must never display “Voice is live.”
- Opening Browser Console or authenticating must never start Voice.
- Keep user-facing prompt text and command aliases in the versioned settings resources, not scattered through automation code.
- Explicit `Codex Project` commands take Codex precedence; plain `Project` commands target ChatGPT even when names collide.

## Required verification

Run these checks for every relevant change:

```bash
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
cd gateway && go test -race ./... && go vet ./...
docker build --network host -t snowball-voice:test .
```

Security-sensitive changes also require authentication, CSRF, origin, settings-validation, and nginx console-gate tests. Do not bypass a failing gate to publish an image.
