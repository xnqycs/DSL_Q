package engine

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCaptureDefaultsMigration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  []string
		migrate bool
	}{
		{"legacy", []string{"dmg", "pkg", "zip", "7z", "rar", "tar", "gz", "xz", "iso"}, true},
		{"custom", []string{"zip", "private"}, false},
		{"empty", []string{}, false},
		{"null", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			key := make([]byte, 32)
			store, err := openStore(dir, key)
			if err != nil {
				t.Fatal(err)
			}
			settings := Defaults()
			settings.CaptureExtensions = tc.values
			settings.AutoCapture = true
			settings.CaptureMinBytes = 123
			settings.ExcludedHosts = []string{"example.test"}
			if err = store.put("settings", settings); err != nil {
				t.Fatal(err)
			}
			store.close()
			m, err := New(dir, key, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := m.Snapshot().Settings
			want := tc.values
			if tc.migrate {
				want = DefaultCaptureExtensions()
			}
			if !slices.Equal(got.CaptureExtensions, want) || !got.AutoCapture || got.CaptureMinBytes != 123 || got.ExcludedHosts[0] != "example.test" {
				t.Fatalf("unexpected migration: %+v", got)
			}
			rows, err := m.store.all()
			if err != nil {
				t.Fatal(err)
			}
			var saved Settings
			if err = json.Unmarshal(rows["settings"], &saved); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(saved.CaptureExtensions, want) {
				t.Fatal("migration not persisted")
			}
			m.Close()
		})
	}
}
func TestNormalizeCaptureExtensions(t *testing.T) {
	got := NormalizeCaptureExtensions([]string{" .ZIP，.tar.GZ; PDF\nzip", "； .png", ""})
	if !slices.Equal(got, []string{"zip", "tar.gz", "pdf", "png"}) {
		t.Fatal(got)
	}
}
