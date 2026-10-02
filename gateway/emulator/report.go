package emulator

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SuiteReport represents the aggregate results of running an emulation test suite.
type SuiteReport struct {
	Timestamp   time.Time        `json:"timestamp"`
	TotalCases  int              `json:"totalCases"`
	PassedCases int              `json:"passedCases"`
	FailedCases int              `json:"failedCases"`
	Duration    time.Duration    `json:"duration"`
	Results     []ScenarioResult `json:"results"`
}

// GenerateMarkdownReport produces a formatted Markdown summary of the test suite results.
func GenerateMarkdownReport(report SuiteReport) string {
	var sb strings.Builder

	sb.WriteString("# Snowball Speaker Emulation Test Report\n\n")
	sb.WriteString(fmt.Sprintf("**Date:** %s  \n", report.Timestamp.Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("**Total Scenarios:** %d | **Passed:** %d | **Failed:** %d | **Total Duration:** %s\n\n",
		report.TotalCases, report.PassedCases, report.FailedCases, report.Duration.Round(time.Millisecond)))

	// Summary status banner
	if report.FailedCases == 0 {
		sb.WriteString("> **Verdict: ALL EMULATION TESTS PASSED**\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("> **Verdict: %d TEST(S) FAILED**\n\n", report.FailedCases))
	}

	// Matrix table
	sb.WriteString("## Scenario Execution Matrix\n\n")
	sb.WriteString("| Scenario | Result | Duration | Time to Connect | Time to Executed | Pre-Voice Captured / Overwritten / Skipped / Replayed | Uplink Sent | Status |\n")
	sb.WriteString("|---|---|---|---|---|---|---|---|\n")

	for _, res := range report.Results {
		statusIcon := "PASS"
		if !res.Passed {
			statusIcon = "FAIL"
		}

		preVoice := fmt.Sprintf("%d / %d / %d / %d", res.PreservedFrames, res.DroppedFrames, res.SkippedFrames, res.ReplayedFrames)
		sb.WriteString(fmt.Sprintf("| `%s` | **%s** | %s | %s | %s | %s | %d frames | %d |\n",
			res.ScenarioName,
			statusIcon,
			res.Duration.Round(time.Millisecond),
			res.TimeToConnect.Round(time.Millisecond),
			res.TimeToExecuted.Round(time.Millisecond),
			preVoice,
			res.UplinkFramesSent,
			res.Receipt.Status,
		))
	}

	sb.WriteString("\n## Scenario Details & Analysis\n\n")

	for i, res := range report.Results {
		sb.WriteString(fmt.Sprintf("### %d. Scenario: `%s`\n\n", i+1, res.ScenarioName))
		sb.WriteString(fmt.Sprintf("- **Outcome:** %t\n", res.Passed))
		if res.Error != "" {
			sb.WriteString(fmt.Sprintf("- **Error:** `%s`\n", res.Error))
		}
		sb.WriteString(fmt.Sprintf("- **Total Duration:** %s\n", res.Duration.Round(time.Millisecond)))
		sb.WriteString(fmt.Sprintf("- **WebRTC Connect Time:** %s\n", res.TimeToConnect.Round(time.Millisecond)))
		sb.WriteString(fmt.Sprintf("- **Command Execution Latency:** %s\n", res.TimeToExecuted.Round(time.Millisecond)))
		sb.WriteString(fmt.Sprintf("- **Pre-Voice Buffer:** %d frames captured, %d overwritten, %d intentionally skipped, %d replayed\n",
			res.PreservedFrames, res.DroppedFrames, res.SkippedFrames, res.ReplayedFrames))
		sb.WriteString(fmt.Sprintf("- **Uplink Frames Delivered:** %d frames (%0.2fs audio @ 32ms/frame)\n",
			res.UplinkFramesSent, float64(res.UplinkFramesSent)*0.032))
		sb.WriteString(fmt.Sprintf("- **Downlink Frames Captured:** %d frames (signal frames: %d)\n",
			res.DownlinkFramesRecv, res.DownlinkSignalFrames))

		if len(res.States) > 0 {
			sb.WriteString("- **State Lifecycle:**\n")
			for _, st := range res.States {
				sb.WriteString(fmt.Sprintf("  - `%s` -> `%s` (%s)\n", st.From, st.To, st.Reason))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// GenerateJSONReport serializes the suite report to indented JSON.
func GenerateJSONReport(report SuiteReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
