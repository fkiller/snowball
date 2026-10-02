# Public branding

Products: Snowball-Voice (ESP32 client), Snowball-Voice-Gate (Gateway/PWA).
Internal container/volume/state identifiers remain unchanged.

Public assets are `public/branding/banner.png` and `public/branding/icon.png`.
The user supplied the reference artwork and authorized public edits. Original
root `banner.png`/`icon.png` are preserved locally and ignored because they
contain the OpenAI logo. The replacements remove that mark and use an original
vertical audio waveform. OpenAI's [brand guidelines](https://openai.com/brand/)
do not permit incorporating its logo into this project's own branding.

The built-in image generation tool performed two precise-object edits:

1. Banner: replace only the white OpenAI knot on the glowing panel with a
   simple original white rounded-bar audio waveform. Preserve puppy identity,
   headset, orbit trails, neon palette, background, and 3:1 composition;
   no text, watermark, or third-party mark.
2. Icon: replace only the white OpenAI knot with the banner's original audio
   waveform; preserve puppy, headset, rounded square, black background, neon
   trails/colors; no text, watermark, or third-party mark.

The root MIT license applies to project-authored assets to the extent rights
are held by the contributors; it never licenses another party's trademarks.
