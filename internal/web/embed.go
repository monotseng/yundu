package web

import "embed"

// Dist contains the production frontend built by the web workspace.
//
//go:embed dist
var Dist embed.FS
