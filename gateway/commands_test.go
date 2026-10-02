package main

import (
	"encoding/json"
	"testing"
)

func testSettings(t *testing.T) snowballSettings {
	t.Helper()
	var settings snowballSettings
	if err := json.Unmarshal(defaultSettingsJSON, &settings); err != nil {
		t.Fatal(err)
	}
	if err := validateSettings(&settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestInterpretWakeCommands(t *testing.T) {
	settings := testSettings(t)
	tests := []struct {
		transcript string
		action     string
		target     string
		project    string
		voice      string
	}{
		{"ChatGPT", "new_chat", "chatgpt", "", ""},
		{"ChatGPT Resume", "resume", "chatgpt", "", ""},
		{"ChatGPT Project Snowball", "project", "chatgpt", "Snowball", ""},
		{"ChatGPT Codex Project Snowball", "project", "codex", "Snowball", ""},
		{"ChatGPT with Cove", "new_chat", "chatgpt", "", "Cove"},
		{"ChatGPT Project Snowball with Juniper", "project", "chatgpt", "Snowball", "Juniper"},
	}
	for _, test := range tests {
		t.Run(test.transcript, func(t *testing.T) {
			intent := interpretWakeCommand(test.transcript, settings)
			if !intent.Matched || intent.Action != test.action || intent.Target != test.target || intent.ProjectName != test.project || intent.VoiceName != test.voice {
				t.Fatalf("unexpected intent: %#v", intent)
			}
		})
	}
}

func TestControlCommandsRemainEnglishWhenPromptLanguageIsKorean(t *testing.T) {
	settings := testSettings(t)
	intent := interpretWakeCommand("ChatGPT 코덱스 프로젝트 스노우볼", settings)
	if !intent.Matched || intent.Action != "unknown" {
		t.Fatalf("non-English control words were accepted: %#v", intent)
	}
}

func TestExitCommands(t *testing.T) {
	settings := testSettings(t)
	if !isExitCommand("종료", settings) || isExitCommand("계속", settings) {
		t.Fatal("Korean exit resources were not applied")
	}
}

func TestResolveSpokenName(t *testing.T) {
	candidates := []string{"Snowball", "Home Automation", "Voice Lab"}
	tests := map[string]string{
		"snow ball":      "Snowball",
		"snoball":        "Snowball",
		"home atomation": "Home Automation",
		"voice labs":     "Voice Lab",
	}
	for spoken, expected := range tests {
		actual, err := resolveSpokenName(spoken, candidates)
		if err != nil || actual != expected {
			t.Errorf("resolveSpokenName(%q) = %q, %v; want %q", spoken, actual, err, expected)
		}
	}
}

func TestResolveSpokenNameRejectsUnsafeMatches(t *testing.T) {
	if _, err := resolveSpokenName("unknown", []string{"Snowball", "Voice Lab"}); err == nil {
		t.Fatal("unrelated name was accepted")
	}
	if _, err := resolveSpokenName("cats", []string{"cat", "cuts"}); err == nil {
		t.Fatal("ambiguous name was accepted")
	}
}
