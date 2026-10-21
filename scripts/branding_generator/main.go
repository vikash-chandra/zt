package main

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// RGB represents an sRGB color component tuple
type RGB struct {
	R, G, B float64
}

func hexToRGB(hex string) RGB {
	if hex[0] == '#' {
		hex = hex[1:]
	}
	var r, g, b uint8
	fmt.Sscanf(hex, "%02x%02x%02x", &r, &g, &b)
	return RGB{float64(r), float64(g), float64(b)}
}

func clamp(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

// BrandPalette defines color configuration for generating brand logo variations
type BrandPalette struct {
	Filename string
	Title    string
	Category string
	Arrow    string // Golden arrow hex
	Rest     string // Single uniform color for Petals, Text, Icon border
	BG       string // Light background hex
	Desc     string
}

func main() {
	masterPath := "branding/vridhi_logo.jpg"
	f, err := os.Open(masterPath)
	if err != nil {
		fmt.Printf("Error opening master image (%s): %v\n", masterPath, err)
		return
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		fmt.Printf("Error decoding master image: %v\n", err)
		return
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	// Available palettes for future regeneration or expansion
	palettes := []BrandPalette{
		{
			Filename: "vridhi_deep_carmine_gold_arrow.jpg",
			Title:    "Official Vridhi Master: Deep Carmine Rose with Solar Gold Arrow",
			Category: "OFFICIAL_MASTER",
			Arrow:    "#fbbf24", // Bright Solar Gold
			Rest:     "#be123c", // Deep Carmine Rose Red
			BG:       "#ffffff",
			Desc:     "Official approved brand identity: sacred lotus geometry with gold market momentum",
		},
	}

	for idx, p := range palettes {
		outImg := image.NewRGBA(image.Rect(0, 0, w, h))

		arrowRGB := hexToRGB(p.Arrow)
		restRGB := hexToRGB(p.Rest)
		bgRGB := hexToRGB(p.BG)

		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := src.At(x, y)
				r, g, b, _ := c.RGBA()
				r8, g8, b8 := float64(r>>8), float64(g>>8), float64(b>>8)

				// Detect Arrow pixels in the master image
				isArrow := (r8 > 170 && g8 > 120 && b8 < 100) || (r8 > 170 && g8 > 40 && g8 < 165 && b8 < 75)

				// Detect Background pixels
				isBG := (r8 > 240 && g8 > 240 && b8 > 240)

				if isBG {
					outImg.Set(x, y, color.RGBA{uint8(bgRGB.R), uint8(bgRGB.G), uint8(bgRGB.B), 255})
				} else if isArrow {
					origLum := (r8*0.299 + g8*0.587 + b8*0.114) / 255.0
					gr := clamp(arrowRGB.R * (0.6 + 0.6*origLum))
					gg := clamp(arrowRGB.G * (0.6 + 0.6*origLum))
					gb := clamp(arrowRGB.B * (0.6 + 0.6*origLum))
					outImg.Set(x, y, color.RGBA{gr, gg, gb, 255})
				} else {
					lum := (r8*0.299 + g8*0.587 + b8*0.114) / 255.0
					nr := clamp(restRGB.R*(1.0-lum) + bgRGB.R*lum)
					ng := clamp(restRGB.G*(1.0-lum) + bgRGB.G*lum)
					nb := clamp(restRGB.B*(1.0-lum) + bgRGB.B*lum)
					outImg.Set(x, y, color.RGBA{nr, ng, nb, 255})
				}
			}
		}

		outPath := filepath.Join("branding", p.Filename)
		outFile, err := os.Create(outPath)
		if err != nil {
			fmt.Println("Error creating file:", err)
			continue
		}
		jpeg.Encode(outFile, outImg, &jpeg.Options{Quality: 98})
		outFile.Close()
		fmt.Printf("[%02d/%02d] Processed %s (%s)\n", idx+1, len(palettes), p.Filename, p.Title)
	}

	generateBrandPreview(palettes)
}

func generateBrandPreview(palettes []BrandPalette) {
	var sb strings.Builder

	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>VRIDHI — Official Brand Identity</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&family=Space+Grotesk:wght@600;700&display=swap" rel="stylesheet">
<style>
:root {
  --bg-primary: #f8fafc;
  --card-bg: #ffffff;
  --text-dark: #0f172a;
  --text-muted: #64748b;
  --border: #e2e8f0;
  --carmine-rose: #be123c;
  --solar-gold: #fbbf24;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font-family: 'Plus Jakarta Sans', sans-serif;
  background: var(--bg-primary);
  color: var(--text-dark);
  padding: 40px 24px 80px;
}
.header {
  max-width: 1000px;
  margin: 0 auto 36px;
  text-align: center;
}
.badge {
  display: inline-block;
  background: #fff1f2;
  color: var(--carmine-rose);
  font-weight: 700;
  font-size: 12px;
  padding: 6px 16px;
  border-radius: 9999px;
  letter-spacing: 1.5px;
  text-transform: uppercase;
  margin-bottom: 12px;
  border: 1px solid #fecdd3;
}
h1 {
  font-family: 'Space Grotesk', sans-serif;
  font-size: 34px;
  font-weight: 700;
  color: #0f172a;
  margin-bottom: 8px;
}
p.subtitle {
  color: var(--text-muted);
  font-size: 15px;
  max-width: 720px;
  margin: 0 auto;
  line-height: 1.6;
}
.card {
  max-width: 1000px;
  margin: 0 auto;
  background: var(--card-bg);
  border-radius: 20px;
  border: 1px solid var(--border);
  overflow: hidden;
  box-shadow: 0 8px 30px rgba(0,0,0,0.06);
}
.img-container {
  width: 100%;
  background: #ffffff;
  padding: 16px;
  border-bottom: 1px solid var(--border);
}
.img-container img {
  width: 100%;
  height: auto;
  display: block;
  border-radius: 12px;
}
.details {
  padding: 24px 32px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
}
.details h3 {
  font-size: 20px;
  font-weight: 700;
  color: var(--carmine-rose);
}
.swatches {
  display: flex;
  gap: 16px;
  align-items: center;
}
.swatch-group {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
  color: #334155;
}
.swatch {
  width: 22px;
  height: 22px;
  border-radius: 6px;
  border: 1px solid rgba(0,0,0,0.15);
}
</style>
</head>
<body>

<div class="header">
  <div class="badge">Official Brand Identity</div>
  <h1>VRIDHI — Deep Carmine Rose & Solar Gold</h1>
  <p class="subtitle">Symmetrical Sacred Lotus petals and brand typography in <strong>Deep Carmine Rose</strong> with the 4-point UP-DOWN-UP market momentum trendline in <strong>Radiant Solar Gold</strong>.</p>
</div>

<div class="card">
`)

	for _, p := range palettes {
		imgPath := filepath.Join("branding", p.Filename)
		imgData, err := os.ReadFile(imgPath)
		b64 := ""
		if err == nil {
			b64 = base64.StdEncoding.EncodeToString(imgData)
		}

		sb.WriteString(fmt.Sprintf(`  <div class="img-container">
    <img src="data:image/jpeg;base64,%s" alt="%s">
  </div>
  <div class="details">
    <div>
      <h3>%s</h3>
      <p style="color: #64748b; font-size: 14px; margin-top: 4px;">%s</p>
    </div>
    <div class="swatches">
      <div class="swatch-group">
        <div class="swatch" style="background: %s;"></div>
        <span>Petals: %s</span>
      </div>
      <div class="swatch-group">
        <div class="swatch" style="background: %s;"></div>
        <span>Arrow: %s</span>
      </div>
    </div>
  </div>
`, b64, p.Title, p.Title, p.Desc, p.Rest, p.Rest, p.Arrow, p.Arrow))
	}

	sb.WriteString("</div>\n</body>\n</html>")

	os.WriteFile("branding/preview.html", []byte(sb.String()), 0644)
	fmt.Println("Updated branding/preview.html with Base64 embedded official logo!")
}
