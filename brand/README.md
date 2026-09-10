# Islet brand

> Version 2.0, 2026-09-10. Source of truth for how Islet looks and sounds. Tokens in `tokens/`, logos in `logo/`, platform icons in `icons/`, fonts in `fonts/`, social images in `social/`.

## 1. Position

Islet is infrastructure software for engineers. It looks like it: black, white, sharp corners, one accent used only where it means something. Reference points are Vercel, Linear and Raycast for finish, and Tailscale and Cloudflare for trust.

Tagline: **Own your infrastructure.**

## 2. Voice

Direct. Technical. No cuteness, no exclamation marks, no metaphors in the product.

| Say | Do not say |
|---|---|
| "Certificate renews 12 Oct." | "Your certificate will be automatically renewed!" |
| "Postgres is reachable from the internet. Restrict to your IP." | "Warning: potential security vulnerability detected" |
| "Build failed at `npm run build`. Last 30 lines below." | "Oops, something went wrong." |
| "Deployed a1b2c3f to production. 41s." | "Deployment successful! 🎉" |

Rules:
- Sentence case everywhere. No all-caps labels.
- Buttons say what happens: "Add domain", "Restore into new volume", "Block IP".
- State facts, then the action. Errors never apologise.
- Numbers, hashes, paths, commands and hostnames are set in Geist Mono.
- "Islet" in prose. The wordmark is "Islet". The daemon is `isletd`, the CLI is `islet`.

## 3. Colour

Black and white are the brand. Everything else is a signal.

| Token | Light | Dark | Use |
|---|---|---|---|
| bg | `#FFFFFF` | `#0A0A0A` | Page |
| surface | `#FAFAFA` | `#141414` | Cards, sidebar, panels |
| surface-2 | `#F5F5F5` | `#1C1C1C` | Nested surfaces, table stripes |
| border | `#E5E5E5` | `#262626` | Hairlines |
| border-strong | `#8A8A8A` | `#666666` | Inputs (3:1) |
| ink | `#0A0A0A` | `#FAFAFA` | Text, primary buttons |
| ink-muted | `#6B6B6B` | `#A3A3A3` | Secondary text (5.4:1 / 8.6:1) |
| ink-faint | `#A3A3A3` | `#6B6B6B` | Placeholders, disabled. Never carries information |
| accent | `#0A5BFF` | `#4D8DFF` | Links, focus ring, selected row. Nothing else |
| success | `#15803D` | `#4ADE80` | With the word "Running", "Passed", and an icon |
| warning | `#A16207` | `#FBBF24` | With the word and an icon |
| danger | `#DC2626` | `#F87171` | With the word and an icon; destructive buttons |

Rules:
- Primary buttons are black on light, white on dark. Not blue.
- The accent is never used for decoration, headings, icons, or the logo.
- Status is colour plus a word plus an icon. Never colour alone.
- Terminal, logs and code blocks are `#0A0A0A` in both themes.
- Dark mode is not an inversion. Surfaces get lighter as they come forward. Shadows only on floating layers.
- Every pair above passes WCAG AA in its theme; the audit runs in the asset generator.

## 4. Type

Two families, both SIL Open Font License, both self-hosted from `fonts/`. Nothing is loaded from a CDN.

- **Geist** for everything readable, weights 400, 500, 600. Tight tracking on titles (-0.02em).
- **Geist Mono** for everything technical: logs, terminal, env vars, connection strings, IDs, commands.

| Token | Size | Weight | Use |
|---|---|---|---|
| xs | 12px | 400/500 | Table meta, timestamps, badges |
| sm | 13px | 400 | Panel default, table cells, labels |
| md | 14px | 400 | Dialog and settings body |
| base | 16px | 400 | Docs and marketing body |
| lg | 18px | 600 | Section titles |
| xl | 22px | 600 | Page titles |
| 2xl | 28px | 600 | Dashboard numbers, tabular figures |
| 3xl | 44px | 600 | Marketing headings, -0.03em |

Line height 1.45 in the UI, 1.6 in long-form, 1.15 for titles and numbers. Weight carries hierarchy, not colour and not caps. Nothing in the panel is bolder than 600.

## 5. Logo

### The mark

A square with its top-right quarter taken out, and that piece placed outside the corner on its own. One colour, hard corners, no gradients.

```
Mainland: M4 22 H50 V50 H78 V96 H4 Z      (100-unit box)
Fragment: x62 y6 w34 h34
Gutter:   12 left of the fragment, 10 below it
```

### Files

| File | Use |
|---|---|
| `logo/lockup.svg`, `lockup-white.svg` | Primary logo: mark plus "Islet". Website header, README, docs |
| `logo/wordmark.svg`, `wordmark-white.svg` | Text only, when the mark is already on screen |
| `logo/mark.svg`, `mark-white.svg` | Mark alone: avatars, favicons, app icons, 16px contexts |
| `logo/app-icon.svg`, `app-icon-light.svg` | App icon preview with rounded square |
| `logo/png/` | Mark at 256, 512, 1024 in black and white on transparent |

All SVGs are outlined; no font is needed to display them.

### Construction of the lockup

Mark height is 1.04 times the cap height of the wordmark, optically centred on the caps. Gap between mark and wordmark is 0.30 times cap height. Wordmark is "Islet" in Geist 600 with the font's own kerning.

### Rules

- Black on light backgrounds, white on dark. Never the accent, never a gradient, never two colours.
- On photographs or coloured fields: white, or black, whichever contrasts. Never a badge behind it.
- Clear space: half the mark height on every side.
- Minimum: mark 16px, lockup 96px wide. Below 96px, use the mark.
- Do not rotate, outline, shadow, round the corners, or animate it.
- Do not use the mark as an icon inside the UI.

## 6. App icons and favicons

Black square, white mark at 60%. Generated from the same geometry as the SVGs.

| Platform | Files | Notes |
|---|---|---|
| iOS | `icons/ios/AppIcon-1024.png`, `-dark.png`, `-light.png`, `-tinted.png` | 1024, square, opaque; iOS applies the mask. Light variant is inverted; tinted is a white glyph on transparent |
| Android adaptive | `icons/android/ic_launcher_foreground.png`, `_background.png`, `_monochrome.png` | 1024 layers, foreground inside the 66% safe zone |
| Android legacy | `icons/android/legacy/ic_launcher-<density>-<px>.png` | mdpi to xxxhdpi, pre-rounded |
| Play Store | `icons/android/play-store-512.png` | 512, opaque |
| Expo | `icons/expo/icon.png`, `adaptive-icon.png`, `adaptive-icon-monochrome.png`, `splash-icon.png`, `favicon.png` | See `app.json` below |
| Web | `icons/web/icon-192.png`, `icon-512.png`, `icon-maskable-*.png`, `apple-touch-icon.png`, `favicon-16/32/48/64.png`, `favicon.ico` (3 frames), `mstile-150.png` | |
| Social | `social/og-image.png` (1200x630), `social/github-social-preview.png` (1280x640) | |

Expo `app.json`:

```json
{
  "expo": {
    "name": "Islet",
    "slug": "islet",
    "icon": "./assets/icon.png",
    "splash": { "image": "./assets/splash-icon.png", "backgroundColor": "#000000", "resizeMode": "contain" },
    "android": {
      "adaptiveIcon": {
        "foregroundImage": "./assets/adaptive-icon.png",
        "monochromeImage": "./assets/adaptive-icon-monochrome.png",
        "backgroundColor": "#000000"
      }
    },
    "web": { "favicon": "./assets/favicon.png" }
  }
}
```

Web head and manifest:

```html
<link rel="icon" href="/favicon.ico" sizes="48x48">
<link rel="icon" href="/favicon-32.png" type="image/png" sizes="32x32">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="mask-icon" href="/mark.svg" color="#000000">
<meta name="theme-color" content="#0A0A0A">
```

```json
{
  "name": "Islet",
  "short_name": "Islet",
  "background_color": "#000000",
  "theme_color": "#0A0A0A",
  "icons": [
    { "src": "/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable" },
    { "src": "/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

## 7. Interface principles

- **Borders, not shadows.** Panels are defined by a 1px border on a slightly lighter or darker surface. Shadows only on menus, dialogs, toasts.
- **Small radii.** 4px inputs and badges, 6px buttons and cards, 10px dialogs. Nothing pill-shaped except avatars and status dots.
- **Dense by default.** 13px panel text, 1.45 line height. Tables show more rows, not taller rows.
- **Black primary button.** One per view. Secondary buttons are outlined. Destructive buttons are red only when the action is destructive.
- **Terminal is always black.** Logs, exec, code, and the command transparency drawer.
- **Motion answers actions.** 100 to 240ms, one curve. No entrance animations, no hover lift, no gradients moving in the background.
- **Icons are Lucide**, stroke 1.5, 16px in tables, 18px in navigation. Never emoji.
- **Status is three things:** colour, a word, an icon.

## 8. Regenerating

The generator lives outside the repo; it is tooling, not product. The geometry in section 5 and `tokens/tokens.json` under `logo` is enough to reproduce every file exactly.
