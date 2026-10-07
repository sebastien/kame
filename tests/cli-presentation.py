"""Module: cli_presentation — native/WASM presentation acceptance."""

from __future__ import annotations

import base64
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

COMMANDS = ["run", "plan", "inputs", "outputs", "span", "tools", "parse", "fmt", "render", "cat", "cache", "help"]
ENVIRONMENT = {**os.environ, "TERM": "xterm", "NO_COLOR": "", "CLICOLOR": "1", "CLICOLOR_FORCE": "0"}


# Function: invoke
# Run one bounded invocation with explicit streams.
def invoke(host: list[str], args: list[str], cwd: Path, data: bytes = b"", status: int = 0, environment: dict[str, str] | None = None) -> subprocess.CompletedProcess[bytes]:
	result = subprocess.run(host + args, cwd=cwd, input=data, capture_output=True, env=environment or ENVIRONMENT, timeout=15)
	assert result.returncode == status, (host, args, result.returncode, result.stdout, result.stderr)
	return result


def records(result: subprocess.CompletedProcess[bytes]) -> list[dict]:
	assert not result.stderr, result.stderr
	return [json.loads(line) for line in result.stdout.splitlines()]


def payload(rows: list[dict], kind: str) -> dict:
	return next(row for row in rows if row.get("type") == kind)


def decoded(row: dict) -> bytes:
	return base64.b64decode(row["data"]) if row["encoding"] == "base64" else row["data"].encode()


def payload_bytes(rows: list[dict], kind: str) -> bytes:
	matching = [row for row in rows if row.get("type") == kind]
	assert matching, (kind, rows)
	return b"".join(decoded(row) for row in matching)


def terminal(host: list[str], cwd: Path, mode: str, width: int = 80, resize: bool = False) -> bytes:
	master, slave = pty.openpty()
	fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 12, width, 0, 0))
	process = subprocess.Popen(host + ["--output", mode, "--color", "never", "--force", "default"], cwd=cwd, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=slave, env=ENVIRONMENT)
	os.close(slave)
	output = bytearray()
	resized_at = None
	deadline = time.monotonic() + 15
	try:
		while time.monotonic() < deadline:
			if select.select([master], [], [], 0.05)[0]:
				try:
					chunk = os.read(master, 65536)
				except OSError as error:
					if error.errno == errno.EIO:
						break
					raise
				if not chunk:
					break
				output.extend(chunk)
				if resize and b"running" in output:
					fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 4, 40, 0, 0))
					# The child has no controlling terminal in this fixture.
					process.send_signal(signal.SIGWINCH)
					resized_at = len(output)
					resize = False
		assert process.wait(timeout=2) == 0, output
		assert process.stdout.read() == b"", output
		if resized_at is not None:
			assert b"\x1b[1A" not in output[resized_at:], output
	finally:
		if process.poll() is None:
			process.kill()
		process.wait()
		process.stdout.close()
		os.close(master)
	return bytes(output)


def check_host(host: list[str], root: Path) -> None:
	native = len(host) == 1
	project = root / ("native" if len(host) == 1 else "wasm")
	project.mkdir()
	(project / "Makefile.kmk").write_text('type = "debug"\nschema = "v1"\ntask default :\n\tsleep 0.4\n')
	(project / "source.kmk").write_text("v=42\n")
	(project / "artifact").write_bytes(b"\xff\x00unterminated")
	large_artifact = bytes(range(256)) * 1024 + b"unterminated"
	(project / "large-artifact").write_bytes(large_artifact)
	(project / "empty").write_bytes(b"")
	for color, environment, expected in [
		("always", {"NO_COLOR": "1"}, True),
		("never", {"CLICOLOR_FORCE": "1"}, False),
		("auto", {"CLICOLOR_FORCE": "1"}, True),
		("auto", {"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}, False),
		("auto", {"CLICOLOR_FORCE": "1", "TERM": "dumb"}, False),
		("auto", {"CLICOLOR_FORCE": "1", "CLICOLOR": "0"}, False),
	]:
		result = invoke(host, ["--color", color, "do", "inputs"], project, environment={**ENVIRONMENT, **environment})
		assert (b"\x1b[" in result.stdout) == expected, result.stdout
	plain = invoke(host, ["--color", "always", "--diagnostic-format", "plain", "do", "parse", "--lang", "expr"], project, b"[", status=1)
	assert b"\x1b" not in plain.stderr and b"PARSE_ERR" in plain.stderr
	for command in COMMANDS:
		for mode in ["ansi", "text", "json"]:
			result = invoke(host, ["--output", mode, "do", command, "--help"], project)
			assert not result.stderr
			assert b"--output" in result.stdout and b"ansi (default)" in result.stdout
			if mode == "json":
				assert json.loads(result.stdout)["type"] == "help"
			else:
				assert b"\x1b" not in result.stdout
	for mode in ["ansi", "text", "json"]:
		args = ["-o", mode]
		plan = invoke(host, args + ["--color", "always", "do", "plan", "--define", "type=release", "--define", "schema=v2"], project)
		assert not plan.stderr, plan.stderr
		if mode == "json":
			document = json.loads(plan.stdout)
			assert document["schema"] == 1 and document["type"] == "plan", document
			assert document["configuration"] == {"type": "release", "schema": "v2"}, document
		else:
			text = re.sub(rb"\x1b\[[0-9;]*m", b"", plan.stdout)
			assert b'    type: "release"\n' in text and b'    schema: "v2"\n' in text, text
			assert b"  schema: 1\n" not in text and b'  type: "plan"\n' not in text, text
		for command in ["plan", "inputs", "outputs", "span", "tools"]:
			result = invoke(host, args + ["do", command], project)
			assert not result.stderr
			if mode == "json":
				json.loads(result.stdout)
			else:
				assert result.stdout.startswith(command.encode())
		result = invoke(host, args + ["do", "parse", "--lang", "expr"], project, b"42")
		assert not result.stderr
		if mode == "json":
			assert json.loads(result.stdout)["ast"]["value"] == 42
		else:
			assert b"integer" in result.stdout
		for command, extra, data, expected, kind in [
			("fmt", [], b"v=42\n", b"v = 42\n", "format-result"),
			("render", ["-c", "hello"], b"", b"hello", "render-result"),
			("cat", ["./artifact"], b"", b"\xff\x00unterminated", "artifact"),
			("cat", ["./large-artifact"], b"", large_artifact, "artifact"),
			("cat", ["./empty"], b"", b"", "artifact"),
			("render", ["-c", ""], b"", b"", "render-result"),
		]:
			result = invoke(host, args + ["do", command] + extra, project, data)
			if mode == "json":
				rows = records(result)
				assert rows[0]["type"] == "invocation-started" and rows[-1]["type"] == "summary"
				assert payload_bytes(rows, kind) == expected, rows
				if native and extra == ["./large-artifact"]:
					assert sum(row.get("type") == kind for row in rows) > 1, rows
			else:
				assert result.stdout == expected and not result.stderr, result
		effects = invoke(host, args + ["do", "run", "-c", '(out "raw-out")', "-c", '(err "raw-err")'], project)
		if mode == "json":
			rows = records(effects)
			assert payload_bytes(rows, "stdout") == b"raw-out" and payload_bytes(rows, "stderr") == b"raw-err", rows
		else:
			# err returns its argument; the session also prints that final value.
			assert effects.stdout == b'raw-out"raw-err"' and effects.stderr == b"raw-err", effects
	rows = records(invoke(host, ["do", "cat", "--json", "-c", 'default = "hello"'], project))
	assert payload(rows, "target-value")["value"] == {"kind": "string", "data": "hello"}
	rows = records(invoke(host, ["do", "fmt", "--json", "--check", "source.kmk"], project, status=1))
	assert payload(rows, "summary")["status"] == "different" and payload(rows, "format-result")["changed"]
	assert payload(rows, "summary")["completed"] == 1
	rows = records(invoke(host, ["do", "fmt", "--json", "-n", "source.kmk"], project, status=1))
	assert "dryRun" not in rows[0] and payload(rows, "summary")["status"] == "different", rows
	rows = records(invoke(host, ["do", "fmt", "--json", "--check", "missing.kmk"], project, status=1))
	assert payload(rows, "summary")["status"] == "failure" and payload(rows, "summary")["failed"] == 1, rows
	for text, changed in [(b"v=42\n", True), (b"v = 42\n", False), (b"", True)]:
		rows = records(invoke(host, ["do", "fmt", "--json", "--check"], project, text, status=int(changed)))
		result = payload(rows, "format-result")
		assert result["source"] == "<stdin>" and result["action"] == "check" and result["changed"] == changed and "data" not in result, rows
		assert payload(rows, "summary")["status"] == ("different" if changed else "success"), rows
		result = invoke(host, ["do", "fmt", "--check"], project, text, status=int(changed))
		assert result.stdout == (b"<stdin>\n" if changed else b""), result
	result = invoke(host, ["do", "fmt", "--in-place"], project, b"v=42\n")
	assert result.stdout == b"v = 42\n" and not result.stderr, result
	rows = records(invoke(host, ["do", "fmt", "--json", "--in-place", "source.kmk"], project))
	assert payload(rows, "format-result")["action"] == "in-place" and (project / "source.kmk").read_bytes() == b"v = 42\n"
	assert payload(records(invoke(host, ["do", "render", "--json", "--check", "-c", "hello"], project)), "render-result")["action"] == "check"
	assert payload(records(invoke(host, ["do", "tools", "check", "--json", "default"], project)), "tools-check-result")["tools"] == []
	assert json.loads(invoke(host, ["do", "cache", "list", "--json"], project).stdout) == []
	assert payload(records(invoke(host, ["do", "cache", "clean", "--json"], project)), "cache-clean-result")["removed"] == 0
	assert json.loads(invoke(host, ["--json", "--version"], project).stdout)["type"] == "version"
	for args in [["--json", "-o", "ansi"], ["--json", "--color", "bad"], ["--color", "bad", "--json"], ["-o", "json", "do", "bogus"], ["--json", "do", "parse"]]:
		assert payload(records(invoke(host, args, project, status=2)), "diagnostic")
	for command in ["parse", "inputs", "span", "tools"]:
		args = ["--json", "do", command] + (["--lang", "expr"] if command == "parse" else ["-c", "v = (\n"])
		result = invoke(host, args, project, b"[", status=1)
		assert not result.stderr
		value = json.loads(result.stdout)
		assert (value[0] if isinstance(value, list) else value)["type"] == "diagnostic"
	assert invoke(host, ["do", "run", "--lang", "expr", "-c", '"--json"'], project).stdout == b'"--json"'
	assert invoke(host, ["do", "run", "--lang", "expr", "-c", "42", "--", "--json"], project).stdout == b"42"
	for width in [40, 80]:
		frame = terminal(host, project, "ansi", width)
		assert b"\x1b[1A" in frame and (b"[default]" if native else b"1 running") in frame, frame
		assert b"started [" not in frame and b"finished in" not in frame and b"info [" not in frame, frame
		if native:
			assert len(re.findall(rb"\[default\] +\d+\.\ds - \xe2\x9c\x93", frame)) == 1, frame
			assert b"workers: 1" in frame, frame
			assert b"done [" not in frame and b"error [" not in frame, frame
			# Timer-only updates must not repaint the separator or worker assignment.
			assert frame.count(b"workers") <= 4, frame
			assert b"idle" not in frame and b" waiting" not in frame and b" - building" in frame, frame
			assert b"\x1b[?2026h" in frame and frame.count(b"\x1b[?2026h") == frame.count(b"\x1b[?2026l"), frame
			for batch in frame.split(b"\x1b[?2026h")[1:]:
				transaction = batch.split(b"\x1b[?2026l", 1)[0]
				if re.search(rb"\[default\] +\d+\.\ds - \xe2\x9c\x93", transaction):
					assert b"workers" in transaction and (b"1 ok" if width < 60 else b"1 complete") in transaction, transaction
		else:
			assert frame.count(b"done [default] complete") == 1, frame
		resized = terminal(host, project, "ansi", width, resize=True)
		assert b"running" in resized and (b" - \xe2\x9c\x93" if native else b"done build") in resized, resized
	assert b"\x1b" not in terminal(host, project, "text")
	assert b"\x1b" not in terminal(host, project, "ansi", width=30)
	if native:
		# A pure Kame target executes without ever emitting process-started.
		(project / "Makefile.kmk").write_text("task default :\n")
		pure = terminal(host, project, "ansi")
		assert b"evaluating" in pure and b"kame" in pure, pure
		assert re.search(rb"\[default\] +\d+\.\ds - \xe2\x9c\x93", pure), pure
		(project / "Makefile.kmk").write_text("task default :\n\tsleep 0.4\n")
	fallback = invoke(host, ["--output", "ansi", "--force"], project)
	assert b"started [" not in fallback.stderr and b"process [" not in fallback.stderr and b"info [" not in fallback.stderr, fallback.stderr
	(project / "Makefile.kmk").write_text("task default : broken\ntask broken :\n\texit 7\n")
	failed = invoke(host, ["--output", "ansi", "--color", "always"], project, status=1)
	if native:
		assert b"\x1b[35mbroken\x1b[0m" in failed.stderr and b" - \x1b[1;31mfailed\x1b[0m" in failed.stderr, failed.stderr
		assert b"  RECIPE_FAIL: recipe exited unsuccessfully (status 7)" in failed.stderr, failed.stderr
	else:
		failure_line = next(line for line in failed.stderr.splitlines() if b"error [broken] failed" in line)
		assert b"RECIPE_FAIL" in failure_line and b"status 7" in failure_line and b"\x1b[" in failure_line, failed.stderr
	assert b"started [" not in failed.stderr and b"finished in" not in failed.stderr, failed.stderr
	(project / "Makefile.kmk").write_text("task default :\n\tsleep 0.4\n")
	# A child can own cursor state; once it does, the presenter must stop rewriting.
	(project / "Makefile.kmk").write_text("task default :\n\tsleep 0.15; printf '\\033[31mraw-control' 1>&2; sleep 0.15\n")
	raw = terminal(host, project, "ansi")
	assert b"raw-control" in raw and b"\x1b[1A" not in raw.split(b"raw-control", 1)[1], raw
	(project / "Makefile.kmk").write_text("task default :\n\tsleep 0.4\n")
	watch = subprocess.Popen(host + ["--json", "--watch"], cwd=project, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=ENVIRONMENT, bufsize=0)
	try:
		rows = []
		def wait_idle(cycle: int) -> None:
			deadline = time.monotonic() + 15
			while time.monotonic() < deadline:
				if select.select([watch.stdout], [], [], 1)[0]:
					rows.append(json.loads(watch.stdout.readline()))
					if rows[-1]["type"] == "watch-idle" and rows[-1]["cycle"] == cycle:
						return
			assert False, rows
		wait_idle(1)
		assert payload(rows, "watch-cycle-finished")["completed"] == 1, rows
		(project / "Makefile.kmk").write_text("value = [\n")
		wait_idle(2)
		assert rows[-1]["status"] == "failure", rows
		(project / "Makefile.kmk").write_text("task default :\n\tsleep 0.1\n")
		wait_idle(3)
		assert rows[-1]["status"] == "success", rows
		assert [row["completed"] for row in rows if row["type"] == "watch-cycle-finished"] == [1, 0, 1], rows
	finally:
		watch.send_signal(signal.SIGTERM)
		_, errors = watch.communicate(timeout=10)
		assert not errors, errors


if __name__ == "__main__":
	with tempfile.TemporaryDirectory(prefix="kame-presentation-") as directory:
		for host in [[str(Path(sys.argv[1]).resolve())], ["node", str(Path(sys.argv[2]).resolve())]]:
			check_host(host, Path(directory))
	print("presentation matrix passed")

# EOF
