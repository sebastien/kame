module kame

go 1.26.0

require (
	github.com/reeflective/readline v1.3.0
	golang.org/x/sys v0.45.0
	golang.org/x/term v0.33.0
)

require github.com/rivo/uniseg v0.4.7 // indirect

// Fork with performance fixes proposed upstream (reeflective/readline#125-#128):
// the kame branch of github.com/sebastien/readline, checked out by git-deps in
// deps/readline (see .gitdeps) and linked from ../third_party/readline.
replace github.com/reeflective/readline => ../third_party/readline
