// Package static embeds the public data bundles (lines-vN.json) that the app
// downloads. Files are compiled into the binary with go:embed and served by
// GET /v1/data/{file}. The manifest endpoint computes size and SHA-256 of
// the current bundle from this FS at startup.
package static

import "embed"

// FS holds every *.json file in this directory.
//
//go:embed *.json
var FS embed.FS
