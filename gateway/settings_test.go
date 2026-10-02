package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPersistAndValidate(t *testing.T) {
	directory := t.TempDir()
	store, err := newSettingsStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "settings.json"))
	if err != nil {
		t.Fatalf("default settings were not persisted: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("default settings permissions are %o", info.Mode().Perm())
	}
	next := store.get()
	next.WakeWord.Phrase = "Snowball"
	next.TurnBased.SilenceThresholdMS = 2200
	if err := store.replace(next); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newSettingsStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.get().WakeWord.Phrase != "Snowball" || reloaded.get().TurnBased.SilenceThresholdMS != 2200 {
		t.Fatal("settings did not persist")
	}

	invalid := reloaded.get()
	invalid.TurnBased.SilenceThresholdMS = 100
	if err := reloaded.replace(invalid); err == nil {
		t.Fatal("invalid silence threshold was accepted")
	}
}

func TestSettingsSchemaExposesCommandAndPromptResources(t *testing.T) {
	required := map[string]bool{
		"wakeWord.phrase":                  false,
		"wakeWord.resumeAliases":           false,
		"wakeWord.projectAliases":          false,
		"wakeWord.codexAliases":            false,
		"voice.withAliases":                false,
		"turnBased.silenceThresholdMs":     false,
		"prompts.ko-KR.projectReady":       false,
		"prompts.ko-KR.projectNext":        false,
		"prompts.ko-KR.projectUnavailable": false,
		"prompts.ko-KR.exitCommands":       false,
	}
	for _, descriptor := range settingsSchema() {
		if _, ok := required[descriptor.Path]; ok {
			required[descriptor.Path] = true
		}
	}
	for path, present := range required {
		if !present {
			t.Errorf("settings schema does not expose %s", path)
		}
	}
}
