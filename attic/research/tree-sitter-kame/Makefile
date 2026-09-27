# NOTE: To parse a file tree-sitter parse 'examples/**/*.go' --quiet --stat
prep: build/npm-installed.task
	@

live-%:
	 echo $(wildcard *.kame *.js test/corpus/*.txt) | xargs -n1 echo | entr make $*

scratch: generate
	tree-sitter parse scratchpad.kame

generate: grammar.js
	tree-sitter generate

test: generate .FORCE
	tree-sitter test

build/parser.wasm: grammar.js
	tree-sitter build --wasm --output $@ .

build/npm-installed.task:
	@npm init
	npm install --save nan
	npm install --save-dev tree-sitter-cli
	mkdir -p "$(dir $@)"
	touch "$@"

.FORCE:
.ONESHELL:
# EOF
