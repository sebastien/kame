# Kame's Design

The base motivation for `kame` is that `make` still has no true alternative,
due to its versatility (it wraps shell scripts, which are the de-facto glue
for automation), its ability to support complex and demanding use cases,
and an overall good performance.

While `make` delivers a lot, it is also hard use, with many ways to shoot
yourself in the foot, a syntax that is both a bit arcane and often cumbersome
to use, and a developer experience missing many of the comforts expected today.

Our intention is to keep what works with `make`:

- A versatile tool that can perform tasks (and create files) automatically
  when files changes. Note that here we take file in the general sense, like
  a computerised thing.

- A wrapper around scripting languages (shell, but possible others) that
  eventually performs the tasks.

- A way to capture and track dependencies, so that we can express what to do
  (or redo) when something changes.

- Keep programmability and configurability, so that a few primitives can be
  combined to meet even the most advanced needs.

and then tweak some things:

- Clear separations between tasks (that don't directly produce something) and
  build rules (that produce one or more outputs).

- Better tracking of dependencies, for instance used tools, referenced variables.

- Unintrusive syntax, as `make`'s use of `$()` for its  expression clash with
  shell syntax, leading to many `$$` everywhere.

- Resilient to whitespaces in names, as when that happens in `make`, all hell
  breaks loose.

Overall, we want `kame` to be:

- Relatively similar to `make` so that transition is possible

- Play nice with the shell, which is the main language for "doing stuff", but
  also support other languages (eg. Python, etc).

- Great developer experience, with clear error reporting and troubleshooting
  capabilities.
