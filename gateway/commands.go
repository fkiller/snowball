package main

import (
	"fmt"
	"strings"
	"unicode"
)

type commandIntent struct {
	Matched     bool   `json:"matched"`
	Action      string `json:"action,omitempty"`
	Target      string `json:"target,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	VoiceName   string `json:"voiceName,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
	NextPrompt  string `json:"nextPrompt,omitempty"`
	Language    string `json:"language,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

func cleanCommand(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, ",.!?;:，。！？；：")
	return strings.Join(strings.Fields(value), " ")
}

func stripFoldedPrefix(value, prefix string) (string, bool) {
	value = strings.TrimSpace(value)
	prefix = strings.TrimSpace(prefix)
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return value, false
	}
	if len(value) == len(prefix) {
		return "", true
	}
	next, _ := utf8FirstRune(value[len(prefix):])
	if !unicode.IsSpace(next) && !unicode.IsPunct(next) {
		return value, false
	}
	return cleanCommand(value[len(prefix):]), true
}

func utf8FirstRune(value string) (rune, int) {
	for _, character := range value {
		return character, len(string(character))
	}
	return 0, 0
}

func foldedEqualAny(value string, choices []string) bool {
	value = cleanCommand(value)
	for _, choice := range choices {
		if strings.EqualFold(value, cleanCommand(choice)) {
			return true
		}
	}
	return false
}

func stripAnyPrefix(value string, choices []string) (string, bool) {
	for _, choice := range choices {
		if remainder, ok := stripFoldedPrefix(value, choice); ok {
			return remainder, true
		}
	}
	return value, false
}

func extractVoice(value string, aliases []string) (string, string) {
	value = cleanCommand(value)
	lower := strings.ToLower(value)
	for _, alias := range aliases {
		prefix := strings.ToLower(cleanCommand(alias)) + " "
		if strings.HasPrefix(lower, prefix) {
			voice := cleanCommand(value[len(prefix):])
			if voice != "" {
				return "", voice
			}
		}
		needle := " " + strings.ToLower(cleanCommand(alias)) + " "
		if index := strings.LastIndex(lower, needle); index >= 0 {
			command := cleanCommand(value[:index])
			voice := cleanCommand(value[index+len(needle):])
			if voice != "" {
				return command, voice
			}
		}
	}
	return value, ""
}

func renderProjectPrompt(template, projectName string) string {
	return strings.ReplaceAll(template, "{{projectName}}", projectName)
}

func interpretWakeCommand(transcript string, settings snowballSettings) commandIntent {
	result := commandIntent{Language: settings.Language}
	if !settings.WakeWord.Enabled {
		result.Reason = "wake word commands are disabled"
		return result
	}
	remainder, ok := stripFoldedPrefix(cleanCommand(transcript), settings.WakeWord.Phrase)
	if !ok {
		result.Reason = "wake word was not detected"
		return result
	}
	result.Matched = true
	remainder, result.VoiceName = extractVoice(remainder, settings.Voice.WithAliases)
	if result.VoiceName == "" {
		result.VoiceName = settings.Voice.DefaultName
	}

	if remainder == "" {
		result.Action = "new_chat"
		result.Target = "chatgpt"
		return result
	}
	if foldedEqualAny(remainder, settings.WakeWord.ResumeAliases) {
		result.Action = "resume"
		result.Target = "chatgpt"
		return result
	}

	afterCodex, codex := stripAnyPrefix(remainder, settings.WakeWord.CodexAliases)
	if codex {
		if projectName, project := stripAnyPrefix(afterCodex, settings.WakeWord.ProjectAliases); project && projectName != "" {
			prompts := settings.Prompts[settings.Language]
			result.Action = "project"
			result.Target = "codex"
			result.ProjectName = projectName
			result.Prompt = renderProjectPrompt(prompts.ProjectReady, projectName)
			result.NextPrompt = renderProjectPrompt(prompts.ProjectNext, projectName)
			return result
		}
	}
	if projectName, project := stripAnyPrefix(remainder, settings.WakeWord.ProjectAliases); project && projectName != "" {
		prompts := settings.Prompts[settings.Language]
		result.Action = "project"
		result.Target = "chatgpt"
		result.ProjectName = projectName
		result.Prompt = renderProjectPrompt(prompts.ProjectReady, projectName)
		result.NextPrompt = renderProjectPrompt(prompts.ProjectNext, projectName)
		return result
	}

	result.Action = "unknown"
	result.Reason = "wake word was detected but the command was not recognized"
	return result
}

func isExitCommand(transcript string, settings snowballSettings) bool {
	prompts, ok := settings.Prompts[settings.Language]
	return ok && foldedEqualAny(transcript, prompts.ExitCommands)
}

func normalizedName(value string) []rune {
	result := make([]rune, 0, len(value))
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result = append(result, character)
		}
	}
	return result
}

func editDistance(left, right []rune) int {
	if len(left) > len(right) {
		left, right = right, left
	}
	previous := make([]int, len(left)+1)
	current := make([]int, len(left)+1)
	for index := range previous {
		previous[index] = index
	}
	for rightIndex, rightCharacter := range right {
		current[0] = rightIndex + 1
		for leftIndex, leftCharacter := range left {
			cost := 1
			if leftCharacter == rightCharacter {
				cost = 0
			}
			insertion := current[leftIndex] + 1
			deletion := previous[leftIndex+1] + 1
			substitution := previous[leftIndex] + cost
			current[leftIndex+1] = min(insertion, deletion, substitution)
		}
		previous, current = current, previous
	}
	return previous[len(left)]
}

// resolveSpokenName maps an imperfect English transcript to an authoritative
// name exposed by the browser adapter. It deliberately rejects weak or tied
// matches instead of opening an unintended project or selecting a voice.
func resolveSpokenName(spoken string, candidates []string) (string, error) {
	query := normalizedName(spoken)
	if len(query) < 2 || len(query) > 128 || len(candidates) == 0 {
		return "", errorsForNameMatch("no candidates")
	}
	bestName := ""
	bestDistance := len(query) + 129
	tied := false
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		normalized := normalizedName(candidate)
		key := string(normalized)
		if candidate == "" || len(normalized) < 2 || seen[key] {
			continue
		}
		seen[key] = true
		distance := editDistance(query, normalized)
		if distance < bestDistance {
			bestName, bestDistance, tied = candidate, distance, false
		} else if distance == bestDistance {
			tied = true
		}
	}
	if bestName == "" || tied {
		return "", errorsForNameMatch("ambiguous name")
	}
	maximum := 1
	longer := max(len(query), len(normalizedName(bestName)))
	if longer >= 6 {
		maximum = 2
	}
	if longer >= 11 {
		maximum = 3
	}
	if bestDistance > maximum || bestDistance*100 > longer*30 {
		return "", errorsForNameMatch("name is not close enough")
	}
	return bestName, nil
}

func errorsForNameMatch(reason string) error {
	return fmt.Errorf("could not safely match the spoken name: %s", reason)
}
