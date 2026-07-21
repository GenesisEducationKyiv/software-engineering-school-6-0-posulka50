package main

import "embed"

//go:embed migrations
var migrationsFS embed.FS

//go:embed static
var staticFS embed.FS
