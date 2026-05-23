package detectors

import "embed"

// FS contains the built-in compatibility and requirements detector YAML files.
// This lets the CLI ship the detector library inside the binary so that
// compatibility inference and requirement checking work after a normal
// `go install` (or any binary distribution) without needing the source checkout.
//
// The files remain at the repo root under detectors/ for easy contribution
// and PR review (as required by the FLO-235 spec).
//
//go:embed compatibility/*.yaml requirements/*.yaml
var FS embed.FS
