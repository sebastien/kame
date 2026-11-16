module kame

go 1.26.0

require (
	github.com/reeflective/readline v1.3.0
	golang.org/x/sys v0.45.0
	golang.org/x/term v0.33.0
)

require github.com/rivo/uniseg v0.4.7 // indirect

// Fork of v1.3.0 keeping keys typed while the cursor position is probed;
// see ../third_party/readline/KAME-PATCHES.md.
replace github.com/reeflective/readline => ../third_party/readline
