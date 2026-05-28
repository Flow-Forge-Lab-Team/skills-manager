package webui

import "embed"

// Dist holds the triage UI static assets embedded in the skills-manager binary.
//
//go:embed dist/*
var Dist embed.FS
