package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang/freetype"
	"github.com/nfnt/resize"
	flag "github.com/spf13/pflag"
	"golang.org/x/image/font/gofont/gomonobold"
)

const (
	maxDimension  = 30.0
	fontSize      = 16.0
	spacingFactor = 0.75
)

func main() {
	textSource := flag.String("with", "", "file or directory containing text to use for characters")
	bg := flag.String("bg", "00000000", "background color as hex (e.g., 000000, FFFFFF)")
	output := flag.String("as", "", "output file path (if empty, prints to terminal)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: one [options] <image-path>\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	imagePath := flag.Arg(0)
	backgroundColor := parseColor(*bg)

	if *output == "" {
		if err := outputToTerminal(imagePath, *textSource); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	ext := strings.ToLower(filepath.Ext(*output))
	switch ext {
	case ".png":
		if err := outputToPNG(imagePath, *textSource, backgroundColor, *output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case ".html":
		if err := outputToHTML(imagePath, *textSource, backgroundColor, *output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case ".txt", ".ansi":
		if err := outputToText(imagePath, *textSource, backgroundColor, *output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: unsupported output format %s (supported: .png, .html, .txt, .ansi)\n", ext)
		os.Exit(1)
	}

	fmt.Printf("Saved to: %s\n", *output)
}

func parseColor(value string) color.RGBA {
	cleaned := strings.ToLower(strings.TrimSpace(value))

	// Remove 0x prefix if present
	cleaned = strings.TrimPrefix(cleaned, "0x")

	// Expand 3-char hex to 6-char
	if len(cleaned) == 3 {
		cleaned = string([]byte{
			cleaned[0], cleaned[0],
			cleaned[1], cleaned[1],
			cleaned[2], cleaned[2],
		})
	}

	var r, g, b, a uint8
	a = 255 // Default alpha

	if len(cleaned) == 8 {
		fmt.Sscanf(cleaned, "%02x%02x%02x%02x", &r, &g, &b, &a)
	} else if len(cleaned) == 6 {
		fmt.Sscanf(cleaned, "%02x%02x%02x", &r, &g, &b)
	} else {
		fmt.Printf("Invalid color format: %s", value)
		os.Exit(1)
	}

	return color.RGBA{r, g, b, a}
}

func outputToHTML(path string, textSource string, bgColor color.RGBA, outputPath string) error {
	img, err := loadAndResizeImage(path)
	if err != nil {
		return err
	}
	pixels := extractPixels(img)
	chars, err := newCharSource(textSource)
	if err != nil {
		return err
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("cannot create output file: %w", err)
	}
	defer file.Close()

	fmt.Fprintln(file, "<!DOCTYPE html>")
	fmt.Fprintln(file, "<html>")
	fmt.Fprintln(file, "  <head>")
	fmt.Fprintln(file, "    <meta charset=\"UTF-8\">")
	fmt.Fprintln(file, "  </head>")

	fmt.Fprintf(file, "  <body style=\"%smargin:20px;\">\n", func() string {
		if bgColor.A > 0 {
			return fmt.Sprintf("background:rgba(%d,%d,%d,%.2f);", bgColor.R, bgColor.G, bgColor.B, float64(bgColor.A)/255.0)
		}
		return "background:#000;"
	}())
	fmt.Fprintln(file, "    <pre style=\"font-family:'Courier New',Courier,monospace;font-size:8px;line-height:10px;margin:0;white-space:pre;\">")

	for _, row := range pixels {
		for _, pixelColor := range row {
			r, g, b, a := pixelColor.RGBA()

			// Output transparent pixels as spaces.
			if a == 0 {
				fmt.Fprint(file, "  ")
				continue
			}

			// Characters are typically twice as tall as wide,
			// so two characters are outputted to compensate.

			char1 := chars.NextChar()
			char2 := chars.NextChar()

			// Convert to 8-bit RGB
			r8 := uint8(r >> 8)
			g8 := uint8(g >> 8)
			b8 := uint8(b >> 8)

			fmt.Fprintf(file, "<span style=\"color:rgb(%d,%d,%d)\">%c%c</span>", r8, g8, b8, char1, char2)
		}
		fmt.Fprintln(file)
	}

	fmt.Fprintln(file, "    </pre>")
	fmt.Fprintln(file, "  </body>")
	fmt.Fprintln(file, "</html>")

	return nil
}

func outputToText(path string, textSource string, bgColor color.RGBA, outputPath string) error {
	img, err := loadAndResizeImage(path)
	if err != nil {
		return err
	}
	pixels := extractPixels(img)
	chars, err := newCharSource(textSource)
	if err != nil {
		return err
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("cannot create output file: %w", err)
	}
	defer file.Close()
	bgPrefix := ""
	if bgColor.A > 0 {
		bgPrefix = fmt.Sprintf("\033[48;2;%d;%d;%dm", bgColor.R, bgColor.G, bgColor.B)
	}
	for _, row := range pixels {
		for _, pixelColor := range row {
			r, g, b, a := pixelColor.RGBA()

			// Output transparent pixels as spaces.
			if a == 0 {
				if bgPrefix != "" {
					fmt.Fprintf(file, "%s  \033[0m", bgPrefix)
				} else {
					fmt.Fprint(file, "  ")
				}
				continue
			}

			// Characters are typically twice as tall as wide,
			// so two characters are outputted to compensate.

			char1 := chars.NextChar()
			char2 := chars.NextChar()

			// Convert to 8-bit RGB
			r8 := uint8(r >> 8)
			g8 := uint8(g >> 8)
			b8 := uint8(b >> 8)

			// Write with ANSI 24-bit color (foreground and optionally background)
			if bgPrefix != "" {
				fmt.Fprintf(file, "%s\033[38;2;%d;%d;%dm%c%c\033[0m", bgPrefix, r8, g8, b8, char1, char2)
			} else {
				fmt.Fprintf(file, "\033[38;2;%d;%d;%dm%c%c\033[0m", r8, g8, b8, char1, char2)
			}
		}
		fmt.Fprintln(file)
	}

	return nil
}

func outputToTerminal(path string, textSource string) error {
	img, err := loadAndResizeImage(path)
	if err != nil {
		return err
	}
	pixels := extractPixels(img)
	chars, err := newCharSource(textSource)
	if err != nil {
		return err
	}
	fmt.Println()
	for _, row := range pixels {
		fmt.Printf(" ")
		for _, pixelColor := range row {
			r, g, b, a := pixelColor.RGBA()

			// Output transparent pixels as spaces.
			if a == 0 {
				fmt.Print("  ")
				continue
			}

			// Characters are typically twice as tall as wide,
			// so two characters are outputted to compensate.
			char1 := chars.NextChar()
			char2 := chars.NextChar()

			// Convert to 8-bit RGB
			r8 := uint8(r >> 8)
			g8 := uint8(g >> 8)
			b8 := uint8(b >> 8)

			// Print both characters with ANSI 24-bit color
			fmt.Printf("\033[38;2;%d;%d;%dm%c%c\033[0m", r8, g8, b8, char1, char2)
		}
		fmt.Println()
	}

	fmt.Println()

	return nil
}

func loadAndResizeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("cannot decode image: %w", err)
	}

	bounds := img.Bounds()
	width := float64(bounds.Dx())
	height := float64(bounds.Dy())
	scale := math.Min(maxDimension/width, maxDimension/height)
	scale = math.Min(scale, 1.0)
	newWidth := uint(width * scale)
	newHeight := uint(height * scale)
	// Scale image as a single character is
	// much bigger than a single pixel.
	resized := resize.Resize(newWidth, newHeight, img, resize.Lanczos3)
	return resized, nil
}

func outputToPNG(path string, textSource string, bgColor color.RGBA, outputPath string) error {
	resized, err := loadAndResizeImage(path)
	if err != nil {
		return err
	}
	pixels := extractPixels(resized)
	chars, err := newCharSource(textSource)
	if err != nil {
		return err
	}
	outputImg, err := renderCharacterImage(pixels, chars, bgColor)
	if err != nil {
		return err
	}
	if err := saveAsPng(outputImg, outputPath); err != nil {
		return err
	}

	return nil
}

// isTextFile checks if a file is text by reading its content
func isTextFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	// Read first 512 bytes for content detection
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}

	// Use http.DetectContentType to determine if it's text
	contentType := http.DetectContentType(buf[:n])
	return strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/xml" ||
		strings.Contains(contentType, "javascript")
}

func findTextFiles(dir string) ([]string, error) {
	var textFiles []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isTextFile(path) {
			textFiles = append(textFiles, path)
		}
		return nil
	})

	return textFiles, err
}

func extractPixels(img image.Image) [][]color.Color {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	pixels := make([][]color.Color, height)
	for y := 0; y < height; y++ {
		pixels[y] = make([]color.Color, width)
		for x := 0; x < width; x++ {
			pixels[y][x] = img.At(bounds.Min.X+x, bounds.Min.Y+y)
		}
	}

	return pixels
}

func renderCharacterImage(pixels [][]color.Color, chars *charSource, bgColor color.RGBA) (image.Image, error) {
	if len(pixels) == 0 || len(pixels[0]) == 0 {
		return nil, fmt.Errorf("invalid pixel dimensions")
	}

	height := len(pixels)
	width := len(pixels[0])
	font, err := freetype.ParseFont(gomonobold.TTF)
	if err != nil {
		return nil, fmt.Errorf("cannot parse font: %w", err)
	}
	cellSize := fontSize * spacingFactor
	outputWidth := int(cellSize * float64(width))
	outputHeight := int(cellSize * float64(height))
	img := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))

	// Fill background
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(font)
	c.SetFontSize(fontSize)
	c.SetClip(img.Bounds())
	c.SetDst(img)

	// Draw characters
	for y, row := range pixels {
		for x, pixelColor := range row {
			r, g, b, a := pixelColor.RGBA()

			// Transparent, nothing to do.
			if a == 0 {
				continue
			}

			// Next character
			char := chars.NextChar()

			c.SetSrc(&image.Uniform{color.RGBA{
				uint8(r >> 8),
				uint8(g >> 8),
				uint8(b >> 8),
				uint8(a >> 8),
			}})

			xPos := int(float64(x) * cellSize)
			yPos := int(float64(y+1) * cellSize) // +1 as freetype uses baseline

			// Draw character
			pt := freetype.Pt(xPos, yPos)
			_, err := c.DrawString(string(char), pt)
			if err != nil {
				return nil, fmt.Errorf("cannot draw character: %w", err)
			}
		}
	}
	return img, nil
}

func saveAsPng(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create output file: %w", err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("cannot encode PNG: %w", err)
	}

	return nil
}

/// Character source

// charSource provides a circular iterator over characters from text files
type charSource struct {
	files       []string
	currentFile int
	chars       []rune
	charIdx     int
}

// newCharSource creates a character source from a file or directory path
func newCharSource(path string) (*charSource, error) {
	if path == "" {
		return &charSource{chars: []rune{'a'}}, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("text source not found: %w", err)
	}

	var textFiles []string
	if info.IsDir() {
		textFiles, err = findTextFiles(path)
		if err != nil {
			return nil, err
		}
	} else {
		textFiles = []string{path}
	}

	if len(textFiles) == 0 {
		return nil, fmt.Errorf("no text files found")
	}

	cs := &charSource{
		files:       textFiles,
		currentFile: 0,
	}

	// Load first file
	if err := cs.loadFile(0); err != nil {
		return &charSource{chars: []rune{'a'}}, nil
	}

	return cs, nil
}

// loadFile loads characters from a specific file
func (cs *charSource) loadFile(index int) error {
	content, err := os.ReadFile(cs.files[index])
	if err != nil {
		return err
	}

	// Filter out control characters (newlines, tabs, etc.) but keep spaces
	var filtered []rune
	for _, r := range []rune(string(content)) {
		if r == ' ' || (r >= 33 && r <= 126) || r > 126 {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) == 0 {
		return fmt.Errorf("no valid characters in file")
	}

	cs.chars = filtered
	cs.charIdx = 0
	return nil
}

// NextChar returns the next character, cycling through files as needed
func (cs *charSource) NextChar() rune {
	if len(cs.chars) == 0 {
		return 'a'
	}

	char := cs.chars[cs.charIdx]
	cs.charIdx++

	// Wrap around current file
	if cs.charIdx >= len(cs.chars) {
		cs.charIdx = 0
		// Move to next file in rotation
		if len(cs.files) > 1 {
			cs.currentFile = (cs.currentFile + 1) % len(cs.files)
			cs.loadFile(cs.currentFile)
		}
	}

	return char
}
