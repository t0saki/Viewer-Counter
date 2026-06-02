// Package web embeds the static dashboard assets.
package web

import "embed"

//go:embed dashboard
var Dashboard embed.FS
