
# Python package and tests

(deps {*}.py):
	grep import $input | xargs -n1 emit


# Add a tool as a dependency

	{*}.c ← tool=@{compiler.c}`
