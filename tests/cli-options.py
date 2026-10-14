"""Module: cli_options — cross-backend command grammar and effect boundaries."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

COMMANDS = ["run", "plan", "inputs", "outputs", "span", "tools", "parse", "fmt", "render", "cat", "cache", "clean", "help"]


def invoke(host: list[str], args: list[str], cwd: Path, text: str = "", status: int = 0) -> subprocess.CompletedProcess[str]:
	"""Run a bounded invocation and assert its exit status."""
	result = subprocess.run(host + args, cwd=cwd, input=text, text=True, capture_output=True, timeout=15)
	assert result.returncode == status, (host, args, result.returncode, result.stdout, result.stderr)
	return result


def checkHost(host: list[str], project: Path) -> None:
	for command in COMMANDS:
		result = invoke(host, ["do", command, "--help"], project)
		assert ("Usage:" in result.stdout or "Commands:" in result.stdout) and not result.stderr, result
		invoke(host, ["do", command, "--bogus"], project, status=2)
	for command in ["parse", "fmt", "render"]:
		language = ["--lang", "expr"] if command != "render" else ["--comment", "plain"]
		for source in [["-c", "42"], ["--command=42"], ["-"], ["--", "-"]]:
			result = invoke(host, ["do", command, *language, *source], project, "42")
			assert result.stdout, result
		for option in ["-c", "--command", "-f", "--file", "-C", "--directory"]:
			invoke(host, ["do", command, *language, option], project, status=2)
		for tail in [["-c", "42", "-"], ["-c", "", "-c", "42"], ["--file="], ["--directory="]]:
			invoke(host, ["do", command, *language, *tail], project, status=2)
		for source in [["-f", "value.km"], ["--file=value.km"], ["--", "value.km"]]:
			result = invoke(host, ["do", command, *language, *source, "-C", "nested"] if "--" not in source else ["do", command, *language, "-C", "nested", *source], project)
			assert result.stdout, result
		invoke(host, ["do", command, *language, "-C", "nested", "--", "-value.km"], project)
	assert not invoke(host, ["do", "parse", "--lang", "expr", "--check", "-c", "42"], project).stdout
	kash = '$x/cp -pL -- $inputs.0 $outputs.0'
	inline = json.loads(invoke(host, ["do", "parse", "--lang", "kash", "--json", "-c", kash], project).stdout)
	stdin = json.loads(invoke(host, ["do", "parse", "--lang", "kash", "--json", "-"], project, kash).stdout)
	assert inline["ast"] == stdin["ast"] and inline["source"] == "<command:1>", inline
	invoke(host, ["do", "parse", "--lang", "expr", "--check", "-c", "["], project, status=1)
	invoke(host, ["do", "fmt", "--lang", "expr", "--in-place"], project, "42", status=2)
	invoke(host, ["do", "fmt", "--lang", "expr", "--check", "-c", "[1   2]"], project, status=1)
	invoke(host, ["do", "fmt", "--lang", "template", "--comment", "HTML", "-c", "hello"], project)
	for command in ["plan", "inputs", "outputs", "span", "tools"]:
		for tail in [["--retry", "2"], ["--shell", "sh"], ["--watch"], ["-j", "2"], ["-c", "default :", "-c", "default :"], ["-f", "a", "-f", "b"]]:
			invoke(host, ["do", command, *tail], project, status=2)
	for command in ["cat", "tools"]:
		invoke(host, ["do", command, "--watch"], project, status=2)
	invoke(host, ["do", "cat", "-n"], project, status=2)
	for tail in [["check", "-C", ".", "default"], ["-C", ".", "check", "default"]]:
		result = invoke(host, ["do", "tools", *tail, "--json"], project)
		rows = [json.loads(line) for line in result.stdout.splitlines()]
		assert rows[0]["type"] == "invocation-started" and rows[-1]["type"] == "summary", rows
	invoke(host, ["do", "help", "--", "parse"], project)
	invoke(host, ["do", "help", "parse", "extra"], project, status=2)
	for language, text in [("km", '(out! "BAD")'), ("kash", "touch ./BAD"), ("expr", '(write! ./BAD "BAD")')]:
		result = invoke(host, ["do", "run", "--allow-write", "--allow-run", "--lang", language, "-n", "-c", text], project)
		assert not result.stdout and not (project / "BAD").exists(), result
	cache = project / ".kame/cache/tasks/record"
	cache.parent.mkdir(parents=True, exist_ok=True)
	cache.write_text("record")
	result = invoke(host, ["do", "cache", "-C", ".", "clean", "-n", "--json"], project)
	rows = [json.loads(line) for line in result.stdout.splitlines()]
	assert next(row for row in rows if row["type"] == "cache-clean-result")["wouldRemove"] == 1 and cache.exists(), rows
	invoke(host, ["do", "cache", "list", "-n"], project, status=2)
	invoke(host, ["do", "clean", "--keep", "--release", "out"], project, status=2)
	# Empty option values are invalid data, not unknown options or missing tokens.
	for command, prefix, options in [
		("parse", [], ["--lang"]),
		("fmt", [], ["--lang", "--indent", "--indent-width", "--comment"]),
		("plan", [], ["--depth", "--directory"]),
		("run", ["-c", "42"], ["--lang", "--directory", "--jobs", "--timeout", "--capture-limit"]),
		("render", ["-c", "hello"], ["--directory", "--comment"]),
		("cache", ["list"], ["--directory"]),
		("clean", [], ["--directory"]),
	]:
		for option in options:
			for value in [[option, ""], [option + "="]]:
				result = invoke(host, ["do", command, *prefix, *value], project, status=2)
				assert "OPT_VALUE_INVALID" in result.stderr, result
	for language in ["expr", "template", "km"]:
		for option in [["--indent", "spaces"], ["--indent-width", "2", "--indent", "spaces"]]:
			result = invoke(host, ["do", "fmt", "--lang", language, *option, "-c", "42"], project, status=2)
			assert "OPT_CONFLICT" in result.stderr, result
	invoke(host, ["do", "fmt", "--lang", "script", "--indent-width", "2", "-c", "default :"], project, status=2)
	invoke(host, ["do", "fmt", "--lang", "expr", "--comment", "plain", "-c", "42"], project, status=2)
	invoke(host, ["do", "run", "--lang", "expr", "--print-result", "-c", "42"], project, status=2)
	for command, tail in [
		("parse", ["--lang", "expr", "-c", "42"]), ("fmt", ["--lang", "expr", "-c", "42"]),
		("render", ["-c", "hello"]), ("run", ["-c", '(out! "BAD")']),
		("plan", ["-c", "default :"]), ("inputs", ["-c", "default :"]),
		("outputs", ["-c", "default :"]), ("span", ["-c", "default :"]),
		("tools", ["-c", "default :"]), ("cat", ["-c", 'default = "BAD"']),
		("cache", ["clean"]), ("clean", []),
	]:
		for directory in ["absent-directory", "nested/value.km"]:
			result = invoke(host, ["do", command, "-C", directory, *tail], project, status=1)
			assert "FS_ERR" in result.stderr and not result.stdout, result
			invoke(host, ["do", command, "-C", directory, "--help"], project)
			result = invoke(host, ["do", command, "-C", directory, *tail, "--json"], project, status=1)
			assert not result.stderr and '"FS_ERR"' in result.stdout, result
	invoke(host, ["do", "fmt", "--lang", "expr", "-C", "absent-directory", str(project / "nested/value.km")], project, status=1)
	invoke(host, ["do", "parse", "--lang", "expr", "-C", "absent-directory", "--bogus"], project, status=2)
	cache.unlink()


def main() -> None:
	root = Path(__file__).resolve().parents[1]
	native = str(Path(sys.argv[1]).resolve())
	wasm = str(Path(sys.argv[2]).resolve())
	with tempfile.TemporaryDirectory(prefix="cli-options-", dir=os.environ.get("TMPDIR")) as directory:
		project = Path(directory)
		(project / "nested").mkdir()
		for name in ["value.km", "-value.km"]:
			(project / "nested" / name).write_text("42")
		(project / "Makefile.kmk").write_text("default :\n")
		for host in [[native], ["node", wasm]]:
			checkHost(host, project)
		invoke([str(root / "bin/kame")], ["init", "--help"], project)
		assert not (project / "Makefile.bootstrap").exists()
	print("native/WASM CLI option matrix passed")


if __name__ == "__main__":
	main()

# EOF
