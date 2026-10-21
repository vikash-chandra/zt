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

The official brand assets in `branding/` are separated by specific target surfaces:
- **Desktop Navbar Logo**: [`branding/vridhi_logo.jpg`](file:///c:/Users/admin/zt/branding/vridhi_logo.jpg) — Lotus + "VRIDHI" horizontal lockup for desktop navbar.
- **Browser Favicon**: [`branding/vridhi_favicon.jpg`](file:///c:/Users/admin/zt/branding/vridhi_favicon.jpg) — Square app squircle tile for browser tab & `/favicon.ico`.
- **Mobile Navbar Logo**: [`branding/vridhi_mobile_logo.jpg`](file:///c:/Users/admin/zt/branding/vridhi_mobile_logo.jpg) — Compact square squircle tile for mobile header and apple-touch-icon.
- **Master Design Sheet**: [`branding/vridhi_full_sheet.jpg`](file:///c:/Users/admin/zt/branding/vridhi_full_sheet.jpg) — Master reference sheet.
- **Standalone Interactive Viewer**: [`branding/preview.html`](file:///c:/Users/admin/zt/branding/preview.html) — Self-contained Base64 preview requiring no web server.

> [!IMPORTANT]
> The logo lockup and favicon tile are separated into dedicated files to ensure pixel-perfect rendering across desktop navigation, browser tabs, and mobile screens without letterboxing or distortion.

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

### 5.1 Favicon Specification
The favicon uses `branding/vridhi_logo.jpg` exclusively via the server-embedded endpoint:
```html
<link rel="icon" type="image/jpeg" href="/branding/vridhi_logo.jpg">
<link rel="shortcut icon" type="image/jpeg" href="/branding/vridhi_logo.jpg">
```

### 5.2 Header Logo Specification
The header navbar displays `branding/vridhi_logo.jpg` directly:
```html
<img src="/branding/vridhi_logo.jpg" alt="Vridhi Logo" class="vridhi-logo-img" onclick="window.location.href='/zt'" title="Vridhi Algorithmic Trading">
```

### 5.3 Server Decoupling & Go Binary Embedding
In `main.go`, `branding/vridhi_logo.jpg` is embedded directly into the Go binary via `//go:embed branding/vridhi_logo.jpg var vridhiLogoBytes []byte` and served via `/branding/vridhi_logo.jpg` and `/favicon.ico`. In `Dockerfile`, `COPY --from=builder /app/branding ./branding` ensures container filesystem availability.
