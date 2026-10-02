# Security policy

Snowball-Voice-Gate and Snowball-Voice are experimental, LAN-only software.
Only the latest tagged prerelease is considered for fixes; there is no stable
or production-trusted firmware release yet.

## Report a vulnerability privately

Use this repository's **Security → Advisories → Report a vulnerability**
button once private vulnerability reporting is enabled. If that button is
unavailable, do not open a public issue containing exploit details or secrets;
contact the maintainer through the GitHub profile linked by the repository.
There is no guaranteed response SLA. Include the affected commit/version,
steps to reproduce, impact, and a redacted proof of concept.

Never attach `/data`, Chromium profiles, cookies, Wi-Fi passwords, enrollment
tokens, private keys, certificates containing private material, or raw audio.

## Supported boundary

- UI/API/console on the configured private IPv4 LAN address; no WAN listener,
  cloud relay, STUN, or TURN.
- ChatGPT login remains inside Chromium; local administrator authentication
  is separate. Browser Console requires the nginx authentication gate.
- Browser writes require same-origin/CSRF checks. Device events require
  enrolled P-256 identity and replay protection.
- The `/data` volume is a credential store. Preserve it during upgrades,
  restrict access, and keep backups private.
- Chromium currently runs without its native sandbox inside a constrained
  container. Do not expose the container to untrusted networks.

## Development firmware limitations

The current firmware stores development credentials in plaintext NVS and lacks
verified Secure Boot, flash encryption, signed OTA, and anti-rollback. Physical
possession of the board is outside the current protection boundary. Do not use
it as a production-trusted appliance. Normal flashing must never write NVS at
`0x9000`. A factory reset is an explicit separate operation.

See [privacy](docs/PRIVACY.md), [pairing security](docs/CLIENT_DISCOVERY_SECURITY.md),
and [release gates](docs/RELEASE_PLAN.md).
