# License scope and third-party notices

Snowball-Voice-Gate (Gateway/PWA) and the project-authored Snowball-Voice
(ESP32 client) source are licensed under the root [MIT License](LICENSE).
This does **not** relicense dependencies, speech models, container packages,
or third-party names and trademarks. MIT here describes the project's own
contributions, not every byte of a compiled firmware or container image.

## ESP32 firmware

The locked firmware targets Espressif ESP32-S3 hardware. The following
vendor conditions remain applicable when using or redistributing the combined
firmware. Do not advertise the combined image as unrestricted MIT software or
port the restricted components to non-Espressif hardware.

| Component | Locked version | License / restriction |
| --- | --- | --- |
| ESP-IDF | 5.5.5 | Apache-2.0 for the framework; bundled third-party notices also apply |
| `espressif/esp_peer` | 1.2.7 | Espressif Modified MIT; use exclusively with Espressif products; source/binary redistribution for non-Espressif products prohibited |
| `espressif/esp-sr` | 2.4.7 | ESPRESSIF MIT; permission for use on Espressif products; retain notices with speech libraries/models |
| Other managed components | `dependencies.lock` | Individual upstream licenses; retain their exact texts in firmware release archives |

Authoritative terms:

- [esp_peer 1.2.7 license](https://components.espressif.com/components/espressif/esp_peer/versions/1.2.7/license)
- [esp-sr 2.4.7 license](https://components.espressif.com/components/espressif/esp-sr/versions/2.4.7/license)
- [ESP-IDF 5.5.5 license](https://github.com/espressif/esp-idf/blob/v5.5.5/LICENSE)

Exact vendor texts are retained in `LICENSES/`. Managed components are fetched
from the registry and are not vendored into this repository. The release
packager copies discovered license/notice files from the locked managed
components; a maintainer must also review model-specific terms and any
bundled binary library notices before publishing binaries.

## Gateway, web, and container

React is MIT, Playwright is Apache-2.0, and the Pion WebRTC libraries are MIT.
The dependency locks, rather than this summary, identify the complete graph.
Generate an npm SBOM and retain the Go module inventory for every binary release.

The Debian-based image also includes Chromium, nginx, PulseAudio, GStreamer,
noVNC, websockify, and their dependencies. These have distinct licenses,
including copyleft licenses. Image distribution needs package notices and
applicable corresponding-source/relinking obligations assessed per package;
the root MIT license does not satisfy those obligations by itself. Keep the
installed `/usr/share/doc/*/copyright` files in distributed images. Image
publication remains a release gate until its license and source-offer review
is recorded. Publishing this project's source is a separate milestone.

## Branding

ChatGPT and OpenAI marks belong to OpenAI. Snowball is unofficial and is not
affiliated with or endorsed by OpenAI. The root MIT license grants no rights
to third-party trademarks. Do not incorporate OpenAI's logo into Snowball's
product icon or banner; see [OpenAI's brand guidelines](https://openai.com/brand/).
Waveshare and Espressif names identify compatible hardware and dependencies.
