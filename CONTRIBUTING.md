# Contributing

Read [AGENTS.md](AGENTS.md), [architecture](ARCHITECTURE.md), and the relevant
setup instructions before changing Snowball-Voice-Gate or Snowball-Voice.
Open a focused issue/PR with the problem, resulting behavior, and verification.
Contributions are submitted under the root MIT license; preserve third-party
license notices and identify imported code or assets.

Use a separate candidate image and disposable state for development. Never
restart a user's production container, change its Docker daemon, overwrite
`/data`, burn ESP32 eFuses, or flash NVS without explicit authorization.
Do not add public listeners or remove authentication/CSRF/origin protections.

## Checks

On Linux, from the repository root:

```sh
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
(cd gateway && go test -race ./... && go vet ./...)
docker build --network host -t snowball-voice:test .
SNOWBALL_QA_IMAGE=snowball-voice:test ./tools/run-qa-browser-smoke.sh
```

Build firmware with the pinned ESP-IDF 5.5.5 image as documented in
[getting started](docs/GETTING_STARTED.md). Scan all history and tracked release
files with Gitleaks 8.30.1. For authentication/device/settings/nginx changes,
include the relevant security tests and isolated browser smoke result.

Windows can run lint, build, web tests, Go vet, and protocol/emulator tests.
Gateway permission and `/tmp` tests require Linux; a Windows failure is not a
substitute for a passing Linux race suite. See the setup guide for tool installs.

Keep PRs small, add meaningful regression tests for behavior changes, and
update user documentation and HANDOFF.md when state materially changes.
Redact screenshots/logs and avoid uploading audio or account/session material.
