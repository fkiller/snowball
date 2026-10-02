package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

//go:embed resources/default-settings.json
var defaultSettingsJSON []byte

type wakeWordSettings struct {
	Enabled        bool     `json:"enabled"`
	Phrase         string   `json:"phrase"`
	ResumeAliases  []string `json:"resumeAliases"`
	ProjectAliases []string `json:"projectAliases"`
	CodexAliases   []string `json:"codexAliases"`
}

type voiceSettings struct {
	DefaultName string   `json:"defaultName"`
	WithAliases []string `json:"withAliases"`
}

type turnBasedSettings struct {
	SilenceThresholdMS int `json:"silenceThresholdMs"`
	MaxUtteranceMS     int `json:"maxUtteranceMs"`
}

type discoverySettings struct {
	DeviceName           string `json:"deviceName"`
	PairingWindowSeconds int    `json:"pairingWindowSeconds"`
}

type promptResources struct {
	ProjectReady       string   `json:"projectReady"`
	ProjectNext        string   `json:"projectNext"`
	ProjectUnavailable string   `json:"projectUnavailable"`
	ExitCommands       []string `json:"exitCommands"`
}

type snowballSettings struct {
	Version   int                        `json:"version"`
	Language  string                     `json:"language"`
	WakeWord  wakeWordSettings           `json:"wakeWord"`
	Voice     voiceSettings              `json:"voice"`
	TurnBased turnBasedSettings          `json:"turnBased"`
	Discovery discoverySettings          `json:"discovery"`
	Prompts   map[string]promptResources `json:"prompts"`
}

type settingOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type settingDescriptor struct {
	Path        string          `json:"path"`
	Group       string          `json:"group"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Control     string          `json:"control"`
	Minimum     int             `json:"minimum,omitempty"`
	Maximum     int             `json:"maximum,omitempty"`
	Options     []settingOption `json:"options,omitempty"`
}

type settingsStore struct {
	mu       sync.RWMutex
	path     string
	settings snowballSettings
}

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
var englishControlPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 '’-]{0,63}$`)

func englishOnly(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if englishControlPattern.MatchString(strings.Join(strings.Fields(value), " ")) {
			result = append(result, value)
		}
	}
	return result
}

func migrateSettings(settings *snowballSettings) (bool, error) {
	if settings.Version != 1 {
		return false, nil
	}
	settings.Version = 2
	settings.WakeWord.ResumeAliases = englishOnly(settings.WakeWord.ResumeAliases)
	settings.WakeWord.ProjectAliases = englishOnly(settings.WakeWord.ProjectAliases)
	settings.WakeWord.CodexAliases = englishOnly(settings.WakeWord.CodexAliases)
	settings.Voice.WithAliases = englishOnly(settings.Voice.WithAliases)
	if len(settings.WakeWord.ResumeAliases) == 0 {
		settings.WakeWord.ResumeAliases = []string{"resume"}
	}
	if len(settings.WakeWord.ProjectAliases) == 0 {
		settings.WakeWord.ProjectAliases = []string{"project"}
	}
	if len(settings.WakeWord.CodexAliases) == 0 {
		settings.WakeWord.CodexAliases = []string{"codex"}
	}
	if len(settings.Voice.WithAliases) == 0 {
		settings.Voice.WithAliases = []string{"with", "voice"}
	}
	return true, nil
}

func newSettingsStore(stateDir string) (*settingsStore, error) {
	store := &settingsStore{path: filepath.Join(stateDir, "settings.json")}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(store.path)
	needsWrite := errors.Is(err, os.ErrNotExist)
	if needsWrite {
		raw = defaultSettingsJSON
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &store.settings); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	migrated, err := migrateSettings(&store.settings)
	if err != nil {
		return nil, fmt.Errorf("migrate settings: %w", err)
	}
	if err := validateSettings(&store.settings); err != nil {
		return nil, fmt.Errorf("validate settings: %w", err)
	}
	if needsWrite || migrated {
		if err := writePrivateJSON(store.path, store.settings); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func normalizeList(values []string, field string) ([]string, error) {
	if len(values) == 0 || len(values) > 16 {
		return nil, fmt.Errorf("%s must have between 1 and 16 values", field)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" || len(value) > 64 {
			return nil, fmt.Errorf("%s contains an invalid value", field)
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func validateSettings(settings *snowballSettings) error {
	if settings.Version != 2 {
		return errors.New("unsupported settings version")
	}
	settings.Language = strings.TrimSpace(settings.Language)
	if !languagePattern.MatchString(settings.Language) {
		return errors.New("language must be a valid language tag")
	}
	settings.WakeWord.Phrase = strings.Join(strings.Fields(settings.WakeWord.Phrase), " ")
	if len(settings.WakeWord.Phrase) < 2 || len(settings.WakeWord.Phrase) > 32 || !englishControlPattern.MatchString(settings.WakeWord.Phrase) {
		return errors.New("wake word phrase must be a 2–32 character English control phrase")
	}
	var err error
	if settings.WakeWord.ResumeAliases, err = normalizeList(settings.WakeWord.ResumeAliases, "resume aliases"); err != nil {
		return err
	}
	if settings.WakeWord.ProjectAliases, err = normalizeList(settings.WakeWord.ProjectAliases, "project aliases"); err != nil {
		return err
	}
	if settings.WakeWord.CodexAliases, err = normalizeList(settings.WakeWord.CodexAliases, "Codex aliases"); err != nil {
		return err
	}
	if settings.Voice.WithAliases, err = normalizeList(settings.Voice.WithAliases, "voice aliases"); err != nil {
		return err
	}
	for field, aliases := range map[string][]string{
		"resume aliases":  settings.WakeWord.ResumeAliases,
		"project aliases": settings.WakeWord.ProjectAliases,
		"Codex aliases":   settings.WakeWord.CodexAliases,
		"voice aliases":   settings.Voice.WithAliases,
	} {
		for _, alias := range aliases {
			if !englishControlPattern.MatchString(alias) {
				return fmt.Errorf("%s must use English control words", field)
			}
		}
	}
	settings.Voice.DefaultName = strings.TrimSpace(settings.Voice.DefaultName)
	if len(settings.Voice.DefaultName) > 64 {
		return errors.New("default voice name is too long")
	}
	if settings.TurnBased.SilenceThresholdMS < 500 || settings.TurnBased.SilenceThresholdMS > 10_000 {
		return errors.New("silence threshold must be between 500 and 10000 milliseconds")
	}
	if settings.TurnBased.MaxUtteranceMS < 5_000 || settings.TurnBased.MaxUtteranceMS > 300_000 {
		return errors.New("maximum utterance must be between 5000 and 300000 milliseconds")
	}
	settings.Discovery.DeviceName = strings.TrimSpace(settings.Discovery.DeviceName)
	if settings.Discovery.DeviceName == "" || len(settings.Discovery.DeviceName) > 64 {
		return errors.New("discovery device name must be between 1 and 64 characters")
	}
	if settings.Discovery.PairingWindowSeconds < 60 || settings.Discovery.PairingWindowSeconds > 900 {
		return errors.New("pairing window must be between 60 and 900 seconds")
	}
	if len(settings.Prompts) == 0 || len(settings.Prompts) > 16 {
		return errors.New("at least one prompt language is required")
	}
	for language, prompts := range settings.Prompts {
		if !languagePattern.MatchString(language) || prompts.ProjectReady == "" || prompts.ProjectNext == "" || prompts.ProjectUnavailable == "" {
			return fmt.Errorf("prompt resources for %q are incomplete", language)
		}
		if len(prompts.ProjectReady) > 1000 || len(prompts.ProjectNext) > 1000 || len(prompts.ProjectUnavailable) > 1000 {
			return fmt.Errorf("prompt resources for %q are too long", language)
		}
		exitCommands, err := normalizeList(prompts.ExitCommands, "exit commands")
		if err != nil {
			return fmt.Errorf("prompt resources for %q: %w", language, err)
		}
		prompts.ExitCommands = exitCommands
		settings.Prompts[language] = prompts
	}
	if _, ok := settings.Prompts[settings.Language]; !ok {
		return errors.New("selected language has no prompt resources")
	}
	return nil
}

func (s *settingsStore) get() snowballSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, _ := json.Marshal(s.settings)
	var copy snowballSettings
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func (s *settingsStore) replace(next snowballSettings) error {
	if err := validateSettings(&next); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writePrivateJSON(s.path, next); err != nil {
		return err
	}
	s.settings = next
	return nil
}

func settingsSchema() []settingDescriptor {
	return []settingDescriptor{
		{Path: "language", Group: "General", Label: "Language", Description: "Language used for turn-based prompts and exit commands.", Control: "select", Options: []settingOption{{Label: "한국어", Value: "ko-KR"}, {Label: "English", Value: "en-US"}}},
		{Path: "wakeWord.enabled", Group: "Wake word", Label: "Enable wake word", Description: "Allow paired clients to submit wake-word commands.", Control: "toggle"},
		{Path: "wakeWord.phrase", Group: "Wake word", Label: "Wake phrase", Description: "Phrase that must begin every hands-free command.", Control: "text"},
		{Path: "wakeWord.resumeAliases", Group: "Wake word", Label: "Resume aliases", Description: "Comma-separated words that continue the current conversation.", Control: "list"},
		{Path: "wakeWord.projectAliases", Group: "Wake word", Label: "Project aliases", Description: "Comma-separated words that select a ChatGPT Project.", Control: "list"},
		{Path: "wakeWord.codexAliases", Group: "Wake word", Label: "Codex aliases", Description: "Comma-separated words that distinguish a Codex project.", Control: "list"},
		{Path: "voice.defaultName", Group: "Voice", Label: "Default voice", Description: "Preferred ChatGPT voice name; leave blank to keep the current voice.", Control: "text"},
		{Path: "voice.withAliases", Group: "Voice", Label: "Voice command aliases", Description: "Comma-separated words used before a voice name.", Control: "list"},
		{Path: "turnBased.silenceThresholdMs", Group: "Turn-based mode", Label: "Silence threshold", Description: "Silence duration that completes an utterance.", Control: "number", Minimum: 500, Maximum: 10000},
		{Path: "turnBased.maxUtteranceMs", Group: "Turn-based mode", Label: "Maximum utterance", Description: "Maximum recording duration for one project request.", Control: "number", Minimum: 5000, Maximum: 300000},
		{Path: "discovery.deviceName", Group: "Discovery", Label: "Device name", Description: "Name advertised to future paired clients.", Control: "text"},
		{Path: "discovery.pairingWindowSeconds", Group: "Discovery", Label: "Pairing window", Description: "How long an explicitly opened pairing window remains available.", Control: "number", Minimum: 60, Maximum: 900},
		{Path: "prompts.ko-KR.projectReady", Group: "Korean resources", Label: "Project ready prompt", Description: "Use {{projectName}} where the project name should appear.", Control: "textarea"},
		{Path: "prompts.ko-KR.projectNext", Group: "Korean resources", Label: "Next request prompt", Description: "Prompt spoken after an answer is read aloud.", Control: "textarea"},
		{Path: "prompts.ko-KR.projectUnavailable", Group: "Korean resources", Label: "Unavailable prompt", Description: "Prompt spoken when a project cannot be opened.", Control: "textarea"},
		{Path: "prompts.ko-KR.exitCommands", Group: "Korean resources", Label: "Exit commands", Description: "Comma-separated phrases that end project mode.", Control: "list"},
		{Path: "prompts.en-US.projectReady", Group: "English resources", Label: "Project ready prompt", Description: "Use {{projectName}} where the project name should appear.", Control: "textarea"},
		{Path: "prompts.en-US.projectNext", Group: "English resources", Label: "Next request prompt", Description: "Prompt spoken after an answer is read aloud.", Control: "textarea"},
		{Path: "prompts.en-US.projectUnavailable", Group: "English resources", Label: "Unavailable prompt", Description: "Prompt spoken when a project cannot be opened.", Control: "textarea"},
		{Path: "prompts.en-US.exitCommands", Group: "English resources", Label: "Exit commands", Description: "Comma-separated phrases that end project mode.", Control: "list"},
	}
}
