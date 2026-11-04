package engine

import (
	"slices"
	"strings"
	"unicode"
)

// Keep the initial UI/extension defaults in sync; checked by the extension tests.
const captureExtensions = `zip 7z rar tar gz bz2 xz zst tgz tbz2 txz lz lzma lz4 cab
dmg pkg iso img exe msi msix apk deb rpm appimage
pdf doc docx xls xlsx ppt pptx odt ods odp rtf txt csv tsv epub mobi azw3
mp3 m4a aac wav flac ogg opus mp4 mkv mov avi webm m4v mpeg mpg ts
jpg jpeg png gif webp heic heif avif tif tiff bmp svg ico psd
json yaml yml xml toml ini log sql db sqlite bak patch diff jar war wasm bin`

func DefaultCaptureExtensions() []string { return strings.Fields(captureExtensions) }

func NormalizeCaptureExtensions(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(",，;；", r)
		}) {
			part = strings.TrimLeft(strings.ToLower(part), ".")
			if part != "" && !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	return out
}

func migrateCaptureExtensions(s *Settings) bool {
	legacy := []string{"dmg", "pkg", "zip", "7z", "rar", "tar", "gz", "xz", "iso"}
	current := NormalizeCaptureExtensions(s.CaptureExtensions)
	slices.Sort(legacy)
	slices.Sort(current)
	if !slices.Equal(current, legacy) {
		return false
	}
	s.CaptureExtensions = DefaultCaptureExtensions()
	return true
}
