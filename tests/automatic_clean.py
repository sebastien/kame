"""Module: automatic_clean — native historical ownership and deletion safety."""

from __future__ import annotations

import fcntl
import hashlib
import json
import os
import signal
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

# -----------------------------------------------------------------------------
#
# OPERATIONS
#
# -----------------------------------------------------------------------------

def run(binary: str, root: Path, *args: str, status: int = 0, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
	result = subprocess.run([binary, "-C", str(root), *args], capture_output=True, text=True, timeout=20, env=env)
	assert result.returncode == status, (args, result.returncode, result.stdout, result.stderr)
	return result


def clean(binary: str, root: Path, *, dry: bool = False, status: int = 0, env: dict[str, str] | None = None) -> dict:
	args = [binary, "do", "clean", "-C", str(root), "--json"]
	if dry:
		args.append("--dry-run")
	result = subprocess.run(args, capture_output=True, text=True, timeout=20, env=env)
	assert result.returncode == status, (result.stdout, result.stderr)
	report = json.loads(result.stdout)
	assert report["type"] == "clean-result" and report["status"] == status, report
	return report


def protect(binary: str, root: Path, mode: str, name: str) -> None:
	result = subprocess.run([binary, "do", "clean", "-C", str(root), mode, name], capture_output=True, text=True, timeout=20)
	assert result.returncode == 0, (result.stdout, result.stderr)


def project(root: Path, body: str | None = None) -> None:
	root.mkdir()
	(root / "input").write_text("seed\n")
	(root / "Makefile.kmk").write_text(body or "default : ./build/out\n./build/out : ./input\n\tcp @< @>\n")


def records(root: Path) -> list[Path]:
	return sorted((root / ".kame/outputs").glob("*.record"))


def reasons(report: dict) -> set[str]:
	return {entry["reason"] for entry in report["entries"]}


def wait_file(path: Path, process: subprocess.Popen[str]) -> None:
	deadline = time.monotonic() + 20
	while not path.exists():
		assert process.poll() is None, process.communicate()
		assert time.monotonic() < deadline, f"timed out waiting for {path}"
		time.sleep(0.01)


def stop(process: subprocess.Popen[str]) -> tuple[str, str]:
	if process.poll() is None:
		process.send_signal(signal.SIGTERM)
	try:
		return process.communicate(timeout=20)
	except subprocess.TimeoutExpired:
		process.kill()
		process.communicate()
		raise


def live_boundaries(binary: str, base: Path) -> None:
	root = base / "live-build"
	project(root, "default : ./build/out\n./build/out : ./input\n\tcp @< @>\n\tprintf started > ./started\n\tread reply < ./gate\n")
	os.mkfifo(root / "gate")
	builder = subprocess.Popen([binary, "-C", str(root)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
	other = None
	try:
		wait_file(root / "started", builder)
		assert reasons(clean(binary, root, status=1)) == {"project-busy"}
		before = {p.name: p.read_bytes() for p in records(root)}
		other = subprocess.Popen([binary, "-C", str(root)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
		time.sleep(0.05)
		assert other.poll() is None, other.communicate()
		assert before == {p.name: p.read_bytes() for p in records(root)}
		waiting = subprocess.Popen([binary, "-C", str(root)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
		try:
			time.sleep(0.05)
			assert waiting.poll() is None, waiting.communicate()
		finally:
			stop(waiting)
		assert waiting.returncode != 0
		assert before == {p.name: p.read_bytes() for p in records(root)}
		deadline = time.monotonic() + 20
		while True:
			try:
				fd = os.open(root / "gate", os.O_WRONLY | os.O_NONBLOCK)
				break
			except OSError:
				assert builder.poll() is None and time.monotonic() < deadline
				time.sleep(0.01)
		os.write(fd, b"continue\n")
		os.close(fd)
		out, err = builder.communicate(timeout=20)
		assert builder.returncode == 0, (out, err)
		out, err = other.communicate(timeout=20)
		assert other.returncode == 0, (out, err)
	finally:
		stop(builder)
		if other is not None:
			stop(other)
	assert clean(binary, root)["removed"] == 1

	root = base / "cancelled-build"
	project(root, "default : ./build/out\n./build/out : ./input\n\tcp @< @>\n\tprintf started > ./started\n\tread reply < ./gate\n")
	os.mkfifo(root / "gate")
	builder = subprocess.Popen([binary, "-C", str(root)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
	try:
		wait_file(root / "started", builder)
	finally:
		out, err = stop(builder)
	assert builder.returncode == 1 and "EXEC_CANCELLED" in err, (builder.returncode, out, err)
	assert reasons(clean(binary, root)) == {"uncertain-production"}
	assert (root / "build/out").read_text() == "seed\n"

	root = base / "live-watch"
	project(root)
	with (root / "watch.log").open("w") as log:
		watch = subprocess.Popen([binary, "-C", str(root), "--watch", "--json"], stdout=log, stderr=subprocess.PIPE, text=True)
		try:
			deadline = time.monotonic() + 20
			while '"watch-idle"' not in (root / "watch.log").read_text():
				assert watch.poll() is None and time.monotonic() < deadline
				time.sleep(0.01)
			assert reasons(clean(binary, root, status=1)) == {"project-busy"}
		finally:
			stop(watch)
	assert watch.returncode == 128 + signal.SIGTERM
	assert (root / "build/out").exists()
	# A watch launched by this runner has the same inherited environment guard.
	assert clean(binary, root)["removed"] == 1


def capture_faults(binary: str, base: Path) -> None:
	if not sys.platform.startswith("linux"):
		return
	shim = base / "cleanup-faults.so"
	source = Path(__file__).with_name("automatic_clean_faults.c")
	subprocess.run(["cc", "-shared", "-fPIC", "-Wall", "-Wextra", "-Werror", "-o", str(shim), str(source), "-ldl"], check=True, timeout=20)
	for mode in ("cancel-capture", "retire-crash", "replace-capture", "replace-occupied"):
		root = base / mode
		project(root)
		env = {**os.environ, "LD_PRELOAD": str(shim), "KAME_CLEAN_TEST_FAULT": mode}
		run(binary, root, env=env)
		state = root / ".kame/outputs"
		(state / "test-fault").touch()
		key = hashlib.sha256(b"build/out").hexdigest()
		if mode == "retire-crash":
			result = subprocess.run([binary, "do", "clean", "-C", str(root), "--json"], capture_output=True, text=True, timeout=20, env=env)
			assert result.returncode == -signal.SIGKILL, (result.stdout, result.stderr)
			assert not (root / "build/out").exists() and records(root)
			assert not list(state.glob("*.recovery")), "retirement left an orphan receipt"
			(root / "build/out").write_text("new-writer\n")
			assert reasons(clean(binary, root, env=env)) == {"artifact-changed"}
			assert (root / "build/out").read_text() == "new-writer\n"
			(root / "build/out").unlink()
			assert clean(binary, root, env=env)["missing"] == 1
			run(binary, root, env=env)
			assert clean(binary, root, env=env)["removed"] == 1
		elif mode == "cancel-capture":
			report = clean(binary, root, status=1, env=env)
			assert "cleanup-cancelled" in reasons(report), report
			assert (root / "build/out").read_text() == "seed\n"
			assert records(root) and not list(state.glob("*.artifact"))
			assert not list(state.glob("*.recovery"))
		elif mode == "replace-capture":
			assert reasons(clean(binary, root, env=env)) == {"artifact-changed"}
			assert (root / "build/out").read_text() == "replacement\n"
			assert (root / "build/test-original").read_text() == "seed\n"
			assert records(root) and not list(state.glob("*.artifact"))
		else:
			report = clean(binary, root, status=1, env=env)
			assert reasons(report) == {"recovery-required"}, report
			assert (root / "build/out").read_text() == "new-writer\n"
			assert (state / f"{key}.artifact").read_text() == "replacement\n"
			assert (root / "build/test-original").read_text() == "seed\n"
			assert reasons(clean(binary, root, status=1, env=env)) == {"recovery-required"}


# -----------------------------------------------------------------------------
#
# API
#
# -----------------------------------------------------------------------------

def main(binary: str) -> None:
	with tempfile.TemporaryDirectory(prefix="automatic-clean-", dir="/tmp/opencode") as directory:
		base = Path(directory)
		root = base / "accumulation"
		project(root, "default : ./build/a\n./build/{name} : ./input\n\tcp @< @>\n")
		run(binary, root)
		run(binary, root, "./build/b")
		(root / "build/unrelated").write_text("keep\n")
		before = {p.name: p.read_bytes() for p in records(root)}
		assert len(before) == 2
		assert clean(binary, root, dry=True)["wouldRemove"] == 2
		assert before == {p.name: p.read_bytes() for p in records(root)}
		assert (root / "build/a").exists()
		run(binary, root, "./build/a")
		assert before == {p.name: p.read_bytes() for p in records(root)}, "no-op rebaselined ownership"
		cache = subprocess.run([binary, "do", "cache", "clean", "-C", str(root)], capture_output=True, text=True, timeout=20)
		assert cache.returncode == 0 and len(records(root)) == 2
		assert clean(binary, root)["removed"] == 2
		assert (root / "build/unrelated").read_text() == "keep\n"
		assert (root / "input").exists() and (root / "build").is_dir()
		assert not records(root) and clean(binary, root)["removed"] == 0

		for case in ("edited", "same-byte-replacement", "metadata"):
			root = base / case
			project(root)
			run(binary, root)
			output = root / "build/out"
			if case == "edited":
				output.write_text("important\n")
			elif case == "metadata":
				output.chmod(0o600)
			else:
				copy = root / "replacement"
				shutil.copy2(output, copy)
				os.replace(copy, output)
			assert reasons(clean(binary, root)) == {"artifact-changed"}
			assert output.exists() and len(records(root)) == 1

		root = base / "changed-rules"
		project(root)
		run(binary, root)
		(root / "Makefile.kmk").write_text("default : ./build/out\n")
		assert "current-source-or-external-input" in reasons(clean(binary, root))
		assert (root / "build/out").read_text() == "seed\n"
		(root / "Makefile.kmk").write_text("default :\n")
		assert reasons(clean(binary, root)) == {"build-context-changed"}
		assert len(records(root)) == 1

		root = base / "recipe-edit"
		project(root)
		run(binary, root)
		(root / "Makefile.kmk").write_text("default : ./build/out\n./build/out : ./input\n\tcp @< @> # changed\n")
		assert reasons(clean(binary, root)) == {"build-context-changed"}

		root = base / "keep-release"
		project(root)
		protect(binary, root, "--keep", "./build/out")
		run(binary, root)
		assert reasons(clean(binary, root)) == {"explicitly-protected"}
		protect(binary, root, "--release", "./build/out")
		assert not records(root) and (root / "build/out").exists()
		run(binary, root)
		assert not records(root), "reuse acquired released output"
		run(binary, root, "--force")
		assert reasons(clean(binary, root)) == {"explicitly-protected"}

		root = base / "partial-group"
		project(root, "default : ./a\n./a ./b : ./input\n\tcp ./input ./a\n\texit 1\n")
		run(binary, root, status=1)
		assert (root / "a").exists()
		assert reasons(clean(binary, root)) == {"uncertain-production"}
		assert len(records(root)) == 2

		root = base / "group-removal"
		project(root, "default : ./a\n./a ./b : ./input\n\tcp ./input ./a\n\tcp ./input ./b\n")
		run(binary, root)
		(root / "Makefile.kmk").write_text("default : ./a\n./a : ./input\n\tcp ./input ./a\n")
		assert reasons(clean(binary, root)) == {"build-context-changed"}
		assert (root / "a").exists() and (root / "b").exists()

		root = base / "copied-root"
		project(root)
		run(binary, root)
		copy = base / "copy"
		shutil.copytree(root, copy)
		assert "ledger-invalid" in reasons(clean(binary, copy, status=1))
		assert (copy / "build/out").exists()

		root = base / "parent-link"
		project(root)
		run(binary, root)
		(root / "build").rename(root / "original-build")
		victim = base / "victim"
		victim.mkdir()
		(victim / "out").write_text("valuable\n")
		(root / "build").symlink_to(victim, target_is_directory=True)
		assert "unsafe-or-unsupported-host-operation" in reasons(clean(binary, root, status=1))
		assert (victim / "out").read_text() == "valuable\n"
		assert (root / "original-build/out").exists()

		root = base / "corrupt-record"
		project(root)
		run(binary, root)
		record = records(root)[0]
		data = bytearray(record.read_bytes())
		data[0] ^= 1
		record.write_bytes(data)
		assert reasons(clean(binary, root, status=1)) == {"ledger-invalid"}
		assert (root / "build/out").exists()

		root = base / "busy"
		project(root)
		run(binary, root)
		with (root / ".kame/outputs/lock").open("rb") as lock:
			fcntl.flock(lock, fcntl.LOCK_EX)
			assert reasons(clean(binary, root, status=1)) == {"project-busy"}
		assert clean(binary, root)["removed"] == 1

		root = base / "interrupted-quarantine"
		project(root)
		run(binary, root)
		key = hashlib.sha256(b"build/out").hexdigest()
		state = root / ".kame/outputs"
		(state / f"{key}.recovery").write_text("build/out")
		(root / "build/out").rename(state / f"{key}.artifact")
		assert reasons(clean(binary, root, status=1)) == {"recovery-required"}
		assert (state / f"{key}.artifact").read_text() == "seed\n"
		(root / "build/out").write_text("replacement\n")
		assert reasons(clean(binary, root, status=1)) == {"recovery-required"}
		assert (root / "build/out").read_text() == "replacement\n"

		root = base / "missing"
		project(root)
		run(binary, root)
		(root / "build/out").unlink()
		assert clean(binary, root)["missing"] == 1 and not records(root)

		root = base / "authored-clean"
		project(root, "default : ./build/out\n./build/out : ./input\n\tcp @< @>\nclean :\n\tprintf authored > ./marker\n")
		run(binary, root)
		run(binary, root, "clean")
		assert (root / "marker").read_text() == "authored"
		assert (root / "build/out").exists()
		assert clean(binary, root)["removed"] == 1
		root = base / "automatic-target"
		project(root)
		run(binary, root)
		run(binary, root, "clean")
		assert not (root / "build/out").exists()

		root = base / "graph-clean-target"
		project(root, "default : ./build/out\n./build/out : ./input\n\tcp @< @>\ncleanup : clean\n")
		run(binary, root)
		run(binary, root, "cleanup")
		assert not (root / "build/out").exists(), "automatic target was not available in the graph"

		root = base / "explicit-source"
		project(root)
		run(binary, root, "-f", "Makefile.kmk")
		assert len(records(root)) == 1, "unified session omitted production"
		report = clean(binary, root)
		assert report["removed"] == 1, report

		root = base / "external-output"
		external = base / "external-artifact"
		project(root, f"default : {external}\n{external} : ./input\n\tcp @< @>\n")
		run(binary, root)
		assert len(records(root)) == 1, "external production was forgotten"
		assert reasons(clean(binary, root)) == {"outside-cleanup-scope"}
		assert external.read_text() == "seed\n"
		protect(binary, root, "--release", str(external))
		assert not records(root) and external.exists()

		root = base / "restricted-clean"
		project(root)
		run(binary, root)
		denied = subprocess.run([binary, "do", "run", "-C", str(root), "-f", "Makefile.kmk", "--entry", "clean", f"--allow-read={root}"], capture_output=True, text=True, timeout=20)
		assert denied.returncode == 1 and "CAP_DENIED" in denied.stderr, (denied.stdout, denied.stderr)
		assert (root / "build/out").exists()

		root = base / "yield-and-write"
		project(root, 'default : ./empty\n./empty : ./input\n\t@(write! "./side" "side")\n\t@(yield! "")\n')
		run(binary, root)
		assert (root / "empty").stat().st_size == 0 and (root / "side").read_text() == "side"
		assert len(records(root)) == 2
		assert clean(binary, root)["removed"] == 2

		root = base / "unproduced-input"
		project(root, "default : ./input\n")
		run(binary, root)
		assert not records(root)
		assert clean(binary, root)["removed"] == 0 and (root / "input").exists()

		root = base / "include-edit"
		project(root, "(include ./rules.kmk)\n")
		(root / "rules.kmk").write_text("default : ./build/out\n./build/out : ./input\n\tcp @< @>\n")
		run(binary, root)
		with (root / "rules.kmk").open("a") as source:
			source.write("# authored formatting changed\n")
		assert reasons(clean(binary, root)) == {"build-context-changed"}

		root = base / "invalid-current-source"
		project(root)
		run(binary, root)
		(root / "Makefile.kmk").write_text("default : (\n")
		report = clean(binary, root, status=1)
		assert "build-context-invalid" in reasons(report) and report["preserved"] == 1
		assert (root / "build/out").exists()

		live_boundaries(binary, base)
		capture_faults(binary, base)

	print("automatic clean: safety acceptance passed")


if __name__ == "__main__":
	main(str(Path(sys.argv[1]).resolve()))

# EOF
