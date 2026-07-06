// Package web embeds the Weave IDP single-page frontend so it can be served
// directly from the Go binary, with no external static-file dependency at
// runtime. The dummy index.html shipped today is replaced by the compiled
// Vue.js SPA in a later step.
package web

import "embed"

// Assets is the embedded static frontend, rooted at the web/ directory: that
// is, index.html sits at the root of this filesystem. It is injected into the
// HTTP server rather than referenced as a global, per the engagement rules.
//
//go:embed index.html
var Assets embed.FS
