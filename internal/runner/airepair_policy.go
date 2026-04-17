// airepair_policy.go is the Go snapshot of
// toolchain/airepair_flags.osty. The Osty source is authoritative;
// see generate.go for the long-term automation plan.
package runner

// AiRepairModeResult mirrors toolchain/airepair_flags.osty's
// AiRepairModeResult. Empty Mode + Ok=false means "unknown value".
//
// Osty: toolchain/airepair_flags.osty:11
type AiRepairModeResult struct {
	Mode string
	Ok   bool
}

// AiRepairCaptureModeResult is the --capture-if shape.
//
// Osty: toolchain/airepair_flags.osty:17
type AiRepairCaptureModeResult struct {
	Mode string
	Ok   bool
}

// ParseAiRepairMode accepts the --airepair-mode flag and returns
// the canonical mode name. Empty string resolves to "auto" so that
// an unset flag behaves like `--airepair-mode=auto`.
//
// Osty: toolchain/airepair_flags.osty:30
func ParseAiRepairMode(value string) AiRepairModeResult {
	switch value {
	case "", "auto":
		return AiRepairModeResult{Mode: "auto", Ok: true}
	case "rewrite":
		return AiRepairModeResult{Mode: "rewrite", Ok: true}
	case "parse":
		return AiRepairModeResult{Mode: "parse", Ok: true}
	case "frontend":
		return AiRepairModeResult{Mode: "frontend", Ok: true}
	default:
		return AiRepairModeResult{Mode: "", Ok: false}
	}
}

// ParseAiRepairCaptureMode accepts the --capture-if flag. Empty
// resolves to "residual" (conservative default).
//
// Osty: toolchain/airepair_flags.osty:47
func ParseAiRepairCaptureMode(value string) AiRepairCaptureModeResult {
	switch value {
	case "", "residual":
		return AiRepairCaptureModeResult{Mode: "residual", Ok: true}
	case "changed":
		return AiRepairCaptureModeResult{Mode: "changed", Ok: true}
	case "always":
		return AiRepairCaptureModeResult{Mode: "always", Ok: true}
	default:
		return AiRepairCaptureModeResult{Mode: "", Ok: false}
	}
}

// ShouldCaptureAiRepair decides whether an airepair run warrants
// writing a capture artifact, given the resolved --capture-if mode
// and two facts from the repair result.
//
// Osty: toolchain/airepair_flags.osty:68
func ShouldCaptureAiRepair(captureMode string, changed bool, totalErrorsAfter int) bool {
	switch captureMode {
	case "always":
		return true
	case "changed":
		return changed
	default:
		return totalErrorsAfter > 0
	}
}
