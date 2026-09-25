command = lambda *_: _
def inputs(_): return _


stem = lambda *_: _
def make(_): return _
def out(_): return _
def run(_): return _
def read(_): return _
def emit(_): return _


@command(token=input("secrets.github.token"), format=json)
def github_get(url, token=None):
    return http.get(url, token=token).asJSON()


# We define a rule that will create the list of projects
with make(f"data:github/${stem}/projects"):
    out(github_get(f"org/${stem}"))

# Now we define how to get a project
with make(f"data:github/${stem('org')}/projects/${stem}"):
    out(github_get(f"project/${stem}"))


# And this one is going to be a dynamic rule: we need to resolve one input
# before we can get the new ones.

with make(f"data:github/${stem}/projects/all") as f:
    # TODO: This will only be evaluated at runtime, and that's the problem. Basically
    # that code should be a template, and we should be able to parse the template
    @run(f"data:github/${stem}/projects")
    def foo(projects):
        yield "["
        last = len(projects) - 1
        for i, _ in enumerate(projects):
            # STEM Will change, it's not statefule, we can't do that
            yield read(f"data:github/${stem}/projects/${_['id']}")
            if i < last:
                yield ","
        yield "]"

make("data:github/NZX/projects")
