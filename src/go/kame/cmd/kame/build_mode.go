package main

//so:embed build_mode.h
var build_mode_h string

// The C compiler binds the artifact's mode without changing shared Go source.
// Ordinary Go execution has no artifact mode and retains development.
//so:extern
func kame_build_mode() string { return "development" }

//so:extern
func kame_build_runtime() string { return "Go" }

func buildMode() string { return kame_build_mode() }
func buildRuntime() string { return kame_build_runtime() }
