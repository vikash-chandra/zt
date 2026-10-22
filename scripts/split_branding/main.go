package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
)

type SubImageSupported interface {
	SubImage(r image.Rectangle) image.Image
}

func cropAndSave(src image.Image, rect image.Rectangle, outPath string) error {
	cropped := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(cropped, cropped.Bounds(), src, rect.Min, draw.Src)

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	opts := &jpeg.Options{Quality: 98}
	if err := jpeg.Encode(out, cropped, opts); err != nil {
		return fmt.Errorf("encode jpeg: %w", err)
	}

	fmt.Printf("Saved %s: %dx%d\n", outPath, rect.Dx(), rect.Dy())
	return nil
}

func main() {
	f, err := os.Open("branding/vridhi_full_sheet.jpg")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		panic(err)
	}

	// 1. Logo crop (Left side: Lotus + Golden Zigzag Trend Arrow + VRIDHI text)
	// Bounds: [160, 142] to [689, 625] (529x483)
	logoRect := image.Rect(160, 142, 689, 625)
	if err := cropAndSave(img, logoRect, "branding/vridhi_logo.jpg"); err != nil {
		panic(err)
	}

	// 2. Favicon crop (Right side: Squircle tile with Lotus + Golden Zigzag Trend Arrow)
	// Bounds: [939, 227] to [1281, 569] (342x342)
	favRect := image.Rect(939, 227, 1281, 569)
	if err := cropAndSave(img, favRect, "branding/vridhi_favicon.jpg"); err != nil {
		panic(err)
	}

	// 3. Mobile logo crop (Right side: Squircle tile for mobile navbar & apple-touch-icon)
	if err := cropAndSave(img, favRect, "branding/vridhi_mobile_logo.jpg"); err != nil {
		panic(err)
	}

	fmt.Println("Successfully split branding/vridhi_full_sheet.jpg into all used brand assets!")
}
