// Package catalog embeds the app templates so a fresh install has a catalog
// without network access. Later versions can fetch newer templates from the
// catalog repository at runtime and overlay them.
package catalog

import "embed"

// FS holds apps/<slug>/islet.yaml, apps/<slug>/compose.yaml and
// recipes/<slug>.yaml (multi-step wizards).
//
//go:embed apps/*/islet.yaml apps/*/compose.yaml recipes/*.yaml
var FS embed.FS
