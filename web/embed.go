package web

import "embed"

// Assets contains the built Web UI. The tracked dist/.gitkeep keeps this
// package buildable before npm has produced real assets.
//
//go:embed all:dist
var Assets embed.FS
