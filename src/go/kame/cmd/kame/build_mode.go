package main

// artifactMode is set independently for each artifact by go build -ldflags.
var artifactMode = "development"

func buildMode() string    { return artifactMode }
func buildRuntime() string { return "Go" }
