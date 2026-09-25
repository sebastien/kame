# The idea: don't use the DSL but use the primitives and leverage
# Python's operators to descibe what needs to be done. We can then
# compile to Ninja or some other execution engine.

deps(glob("*.py")) << bash("""
grep import $input | xargs -n1 emit
""")
