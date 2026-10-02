# Privacy and network behavior

LAN-only describes **inbound access to Snowball**, not offline AI processing.

| Data | Where it goes / lives |
| --- | --- |
| Wake-word recognition and bounded command-tail recognition | Local ESP32 speech models |
| Conversation microphone audio | ESP32/browser → LAN Gateway → Chromium → ChatGPT's remote service |
| ChatGPT responses | Remote service → Chromium → LAN Gateway → speaker/browser |
| ChatGPT login/session | Persistent Chromium profile in `/data`; separate from Snowball administrator login |
| Administrator password hash, device registry, CA/private keys | Gateway `/data` volume |
| Wi-Fi credentials and device private key | ESP32 development NVS; currently not encrypted |
| Web Push alerts | Optional browser push provider; may leave the LAN |
| Diagnostic traces | Bounded board RAM and optional host logs; review/redact before sharing |

The ESP32's `network-test` diagnostic also makes an HTTP request to
`example.com` to distinguish LAN reachability from Internet connectivity.
Builds download dependencies from their upstream registries.

ChatGPT account terms, availability, Voice limits, and data handling are
controlled by OpenAI. Snowball does not supply an account, subscription, API
key, or entitlement, and does not bypass sign-in/CAPTCHA. Review the current
[OpenAI terms](https://openai.com/policies/terms-of-use/) and your account's
privacy/data controls before using this unofficial web integration.

The Web Client requests microphone access when you start. The ESP32 must
listen locally for its wake phrase while powered; unplug it to stop local
listening. Opening Browser Console or signing in does not start a Voice session.

Keep `/data` and device backups private. A normal upgrade preserves identity,
credentials, and browser sessions. To retire a board, revoke it in Admin and
use the explicit factory-reset workflow; do not confuse routine flashing with
credential erasure. There is no promise of forensic deletion from third-party
services or previous backups.
