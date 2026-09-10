// Package catalog embeds the app templates so a fresh install has a catalog
// without network access. Later versions can fetch newer templates from the
// catalog repository at runtime and overlay them.
package catalog

import "embed"

// FS holds apps/<slug>/islet.yaml and apps/<slug>/compose.yaml.
//
//go:embed apps/*/islet.yaml apps/*/compose.yaml
var FS embed.FS
