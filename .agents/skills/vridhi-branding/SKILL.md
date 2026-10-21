---
name: vridhi-branding
description: Manage, generate, audit, and implement the official Vridhi brand identity, color system (Deep Carmine Rose & Solar Gold), geometric logo assets, SVG icons, and trading dashboard styling.
---

# VRIDHI Brand Identity & Design System

This skill documents and governs the official **VRIDHI** brand identity, technical asset specifications, vector geometries, color tokens, and integration patterns for the trading bot web dashboard and mobile interface.

---

## 1. Brand Concept & Philosophy

**VRIDHI** (वृद्धि — Sanskrit for *growth, prosperity, compounding, elevation*) unites traditional Indian cultural symbolism with high-precision algorithmic trading technology.

- **Sacred Lotus Geometry**: Represents organic, disciplined, balanced growth rising above market noise.
- **Up-Down-Up Momentum Trendline**: Represents market price action — initial accumulation impulse, pullback consolidation, and breakout continuation into new highs.
- **Strict Geometric Enclosure**: The market trendline is strictly contained *inside the central oval petal*, symbolizing disciplined risk management, capital protection, and bounded volatility.

---

## 2. Official Brand Palette

| Token Name | Hex Code | Role |
| :--- | :--- | :--- |
| **Deep Carmine Rose** | `#be123c` | Primary Brand Color: Lotus petals, typography, headers, primary buttons |
| **Solar Gold** | `#fbbf24` | Accent Wealth Color: Upward market trendline, breakout arrowhead, nodes |
| **Warm Gold Gradient** | `linear-gradient(135deg, #d97706 0%, #f59e0b 50%, #fbbf24 100%)` | High-impact metallic accents, glowing indicators, pulse badges |
| **Charcoal Accent** | `#0f172a` | Text on light backgrounds, dark mode card contrast |
| **Light Canvas** | `#ffffff` / `#fdf2f4` | Primary brand showcase background |
| **Dark Theme Surface**| `#06090F` / `#0D1421` | Institutional dark mode terminal surfaces |

---

## 3. Approved Master Assets

All official brand assets reside in `branding/`:

- **Master High-Res Image**: [`branding/vridhi_logo.jpg`](file:///c:/Users/admin/zt/branding/vridhi_logo.jpg) (also preserved as [`vridhi_deep_carmine_gold_arrow.jpg`](file:///c:/Users/admin/zt/branding/vridhi_deep_carmine_gold_arrow.jpg))
- **Scalable Vector Graphic (SVG)**: [`branding/vridhi_logo.svg`](file:///c:/Users/admin/zt/branding/vridhi_logo.svg) — Crisp at any DPI, retina displays, favicons, and vector signage.
- **Standalone Interactive Viewer**: [`branding/preview.html`](file:///c:/Users/admin/zt/branding/preview.html) — Self-contained Base64 preview requiring no web server.

---

## 4. Logo Generation & Modification Tool

To regenerate assets, change palettes, or export new variants, run the official Go branding engine:

```powershell
go run scripts/branding_generator/main.go
```

The script:
1. Loads the master template.
2. Segments the background, petals/linework, and center market trend arrow.
3. Applies custom HSL/RGB transforms with anti-aliasing preservation.
4. Updates `branding/preview.html` with self-contained Base64 rendering.

---

## 5. UI Application Implementation Standards

When implementing VRIDHI branding in web/mobile dashboards (`index.html`):

### 5.1 Favicon Data URI
The favicon is declared in `<head>` as an inline SVG Data URI for instant zero-latency loading:
```html
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,...">
```

### 5.2 Header Logo & Squircle Tile
The header features:
- A compact squircle icon tile containing the Deep Carmine Rose lotus with Solar Gold trend arrow.
- Typography: `VRIDHI TRADE` with secondary live connection telemetry.

### 5.3 Theme Compatibility
- **Light Theme**: Deep Carmine Rose (`#be123c`) linework with Solar Gold (`#f59e0b`) arrow against `#ffffff`.
- **Dark Theme**: Radiant Rose (`#e11d48`) with glowing Solar Gold (`#fbbf24`) arrow against `#06090F`.
