import type { CaptureSettings } from "./rules.ts";

export const commonCaptureExtensions = `zip 7z rar tar gz bz2 xz zst tgz tbz2 txz lz lzma lz4 cab
dmg pkg iso img exe msi msix apk deb rpm appimage
pdf doc docx xls xlsx ppt pptx odt ods odp rtf txt csv tsv epub mobi azw3
mp3 m4a aac wav flac ogg opus mp4 mkv mov avi webm m4v mpeg mpg ts
jpg jpeg png gif webp heic heif avif tif tiff bmp svg ico psd
json yaml yml xml toml ini log sql db sqlite bak patch diff jar war wasm bin`.split(/\s+/);
export const defaults: CaptureSettings = {
  autoCapture: false, captureMinBytes: 20 * 1024 * 1024,
  captureExtensions: commonCaptureExtensions, excludedHosts: [],
};
export function normalizeExtensions(values: string[]): string[] {
  return [...new Set(values.flatMap(value => value.toLowerCase().split(/[\s,，;；]+/).map(part => part.replace(/^\.+/, "")).filter(Boolean)))];
}
