"""Module: lock_diagnostics — observable, cancellable cross-process lock waits."""

from __future__ import annotations

from contextlib import ExitStack
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import shutil
import signal
import struct
import subprocess
import sys
import termios
import tempfile
import time

SGR = re.compile(rb"\x1b\[[0-9;]*m")

# -----------------------------------------------------------------------------
#
# OPERATIONS
#
# -----------------------------------------------------------------------------

def waitFor(check, process: subprocess.Popen, timeout: float = 8) -> None:
	deadline = time.monotonic() + timeout
	while not check():
		assert process.poll() is None, f"process exited: {process.returncode}"
		assert time.monotonic() < deadline, "timed out waiting for observable progress"
		time.sleep(0.02)


def stop(process: subprocess.Popen) -> None:
	if process.poll() is None:
		os.killpg(process.pid, signal.SIGTERM)
		try:
			process.wait(timeout=3)
		except subprocess.TimeoutExpired:
			os.killpg(process.pid, signal.SIGKILL)
			process.wait(timeout=3)


def start(stack: ExitStack, command: list[str], root: Path, name: str, env=None) -> subprocess.Popen:
	out = stack.enter_context((root / f"{name}.out").open("w"))
	err = stack.enter_context((root / f"{name}.err").open("w"))
	process = subprocess.Popen(command, cwd=root, stdout=out, stderr=err, env=env, start_new_session=True)
	stack.callback(stop, process)
	return process


def events(root: Path, name: str) -> list[dict]:
	return [json.loads(line) for line in (root / f"{name}.out").read_text().splitlines()]


def terminalWait(command: list[str], root: Path, *, color: bool = True, environment: dict | None = None, dashboard: bool = True, width: int = 100) -> None:
	with ExitStack() as stack:
		master, slave = pty.openpty()
		stack.callback(os.close, master)
		stack.callback(os.close, slave)
		fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, width, 0, 0))
		os.set_blocking(master, False)
		out = stack.enter_context((root / "terminal.out").open("w"))
		process = subprocess.Popen(command, cwd=root, stdout=out, stderr=slave, env={**os.environ, "TERM": "xterm", **(environment or {})}, start_new_session=True)
		stack.callback(stop, process)
		log = bytearray()
		def refreshed() -> bool:
			try:
				log.extend(os.read(master, 65536))
			except BlockingIOError:
				pass
			if not dashboard:
				return b"BLOCKED" in log and b"Ctrl-C" in log
			return log.count(b"BLOCKED") >= 2 and b"\x1b[1A" in log and b"waiting " in log
		waitFor(refreshed, process)
		assert b"owner:" in log and str(root).encode() in log, log
		if color:
			assert all(sequence in log for sequence in [b"\x1b[93m", b"\x1b[36m", b"\x1b[1m", b"\x1b[2m"]), log
		else:
			assert not SGR.search(log), log
		plain = SGR.sub(b"", log).decode()
		assert re.search(r"  lock: +/", plain) and re.search(r"  time: +waiting \d+\.\d+s", plain), plain
		if dashboard:
			beforeResize = len(log)
			fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 20, 0, 0))
			process.send_signal(signal.SIGWINCH)
			waitFor(lambda: refreshed() and len(log) > beforeResize, process)
			assert b"\x1b[1A" not in log[beforeResize:], "resize erased reflowed scrollback"
		stop(process)
		assert process.returncode != 0
		assert not (root / "terminal.out").read_text()


def nativeLocks(binary: str, root: Path) -> None:
	root.mkdir()
	(root / "Makefile.kmk").write_text('TOKEN = "default"\ndefault :\n\tprintf "result\\n"\nhold :\n\ttouch ready\n\twhile [ ! -f release ]; do sleep 0.05; done\n')
	with ExitStack() as stack:
		owner = start(stack, [binary, "--define", "TOKEN=secret-not-for-lock-logs", "hold"], root, "owner")
		waitFor(lambda: (root / "ready").exists(), owner)
		text = start(stack, [binary, "--output=text", "--color=always"], root, "text")
		structured = start(stack, [binary, "--json", "--color=always", "-f", "Makefile.kmk"], root, "json")
		watch = start(stack, [binary, "--output=text", "--color=never", "--watch"], root, "watch")
		waitFor(lambda: "BLOCKED" in (root / "text.err").read_text(), text)
		waitFor(lambda: '"type":"lock-waiting"' in (root / "json.out").read_text(), structured)
		waitFor(lambda: "BLOCKED" in (root / "watch.err").read_text(), watch)
		lock = root / ".kame/outputs/lock"
		for name in ["text", "watch"]:
			log = (root / f"{name}.err").read_text()
			assert str(lock) in log and f"PID {owner.pid}" in log and "kame hold" in log, log
			assert "secret-not-for-lock-logs" not in log
		assert not (root / "text.out").read_text(), "wait notice contaminated data stdout"
		assert b"\x1b[93m" in (root / "text.err").read_bytes()
		assert not SGR.search((root / "watch.err").read_bytes())
		report = subprocess.run([binary, "do", "clean", "--json"], cwd=root, capture_output=True, text=True, timeout=3)
		assert report.returncode == 1, report
		busy = json.loads(report.stdout)
		assert busy["type"] == "clean-result" and busy["lock"]["path"] == str(lock), busy
		assert busy["lock"]["owner"]["pid"] == owner.pid, busy
		assert "secret-not-for-lock-logs" not in report.stdout
		protection = subprocess.run([binary, "do", "clean", "--json", "--keep", "./out"], cwd=root, capture_output=True, text=True, timeout=3)
		assert protection.returncode == 1 and str(lock) in protection.stdout and str(owner.pid) in protection.stdout, protection
		if sys.platform == "linux":
			assert busy["lock"]["owner"]["verified"] is True, busy
		stop(watch)
		assert watch.returncode != 0 and owner.poll() is None and lock.exists()
		terminalWait([binary, "--color=always"], root)
		terminalWait([binary, "--color=always"], root, width=40)
		terminalWait([binary, "--color=never"], root, color=False)
		terminalWait([binary], root, color=False, environment={"NO_COLOR": "1"})
		terminalWait([binary], root, color=False, environment={"TERM": "dumb"}, dashboard=False)
		# A long wait must stay visibly alive without flooding redirected logs.
		waitFor(lambda: (root / "text.err").read_text().count("BLOCKED") >= 2, text)
		assert (root / "text.err").read_text().count("BLOCKED") == 2
		(root / "release").touch()
		assert owner.wait(timeout=3) == 0
		assert text.wait(timeout=3) == 0
		assert structured.wait(timeout=3) == 0
		assert (root / "text.out").read_text() == "result\n"
		assert "lock acquired after" in (root / "text.err").read_text()
		assert b"\x1b[32m" in (root / "text.err").read_bytes()
		lockEvents = [event for event in events(root, "json") if event["type"].startswith("lock-")]
		assert lockEvents[0]["type"] == "lock-waiting" and lockEvents[0]["owner"]["pid"] == owner.pid, lockEvents
		assert lockEvents[-1]["type"] == "lock-acquired" and lockEvents[-1]["elapsedMs"] >= 5000, lockEvents
		assert not (root / "json.err").read_text()
		assert not SGR.search((root / "json.out").read_bytes())
		assert lock.read_bytes() == b"", "released lock retained stale owner metadata"


def nativeCache(binary: str, root: Path) -> None:
	root.mkdir()
	(root / "Makefile.kmk").write_text('task cached : ; [shell: kash]\n\t(write! ./side "changed")\n\t(out! "cached")\n')
	seed = subprocess.run([binary, "cached"], cwd=root, capture_output=True, text=True, timeout=5)
	assert seed.returncode == 0, seed
	(root / "side").unlink()
	locks = list((root / ".kame/cache/locks").glob("*"))
	assert locks
	# Simulate an external flock user and stale Kame metadata on the same inode.
	with ExitStack() as stack:
		for path in locks:
			lock = stack.enter_context(path.open("r+b"))
			fcntl.flock(lock, fcntl.LOCK_EX)
			lock.write(b"kame-lock-v1 2147483647 1 1\nstale-owner\n")
			lock.flush()
		before = {path: (path.stat().st_ino, path.read_bytes()) for path in locks}
		for signalName in (signal.SIGINT, signal.SIGTERM):
			name = f"cache-{signalName.name}"
			waiter = start(stack, [binary, "--json", "cached"], root, name)
			waitFor(lambda: '"type":"lock-waiting"' in (root / f"{name}.out").read_text(), waiter)
			event = next(event for event in events(root, name) if event["type"] == "lock-waiting")
			assert event["kind"] == "cache" and Path(event["path"]).is_absolute(), event
			assert "stale-owner" not in json.dumps(event), event
			if sys.platform == "linux":
				assert event["owner"]["pid"] == os.getpid() and event["owner"]["verified"], event
			waiter.send_signal(signalName)
			assert waiter.wait(timeout=3) != 0
			assert not (root / "side").exists(), "cancelled cache waiter committed a write"
			assert not any(event["type"] in ("effect", "stdout", "target-completed") for event in events(root, name)), events(root, name)
			assert any(event.get("diagnostic", {}).get("code") == "EXEC_CANCELLED" for event in events(root, name)), events(root, name)
			assert before == {path: (path.stat().st_ino, path.read_bytes()) for path in locks}, "cancelled waiter changed the holder's lock"
	# Kernel ownership, not diagnostic metadata, controls reacquisition.
	result = subprocess.run([binary, "cached"], cwd=root, capture_output=True, text=True, timeout=5)
	assert result.returncode == 0 and result.stdout == seed.stdout, result
	assert "BLOCKED" not in result.stderr


def nativeHardlinks(binary: str, root: Path) -> None:
	"""Keep hard-linked user files intact during acquisition and release."""
	root.mkdir()
	(root / "Makefile.kmk").write_text('task cached : ; [shell: kash]\n\t(out! "cached")\nhold :\n\ttouch ready\n\twhile [ ! -f release ]; do sleep 0.05; done\n')
	important = root / "important"
	important.write_bytes(b"keep this file\n")
	projectLock = root / ".kame/outputs/lock"
	projectLock.parent.mkdir(parents=True)
	os.link(important, projectLock)
	for options in (["--dry-run"], []):
		result = subprocess.run([binary, "do", "clean", "--json", *options], cwd=root, capture_output=True, text=True, timeout=5)
		assert result.returncode == 1, result
		assert important.read_bytes() == b"keep this file\n", "cleanup truncated a hard-linked project file"
	projectLock.unlink()
	seed = subprocess.run([binary, "cached"], cwd=root, capture_output=True, text=True, timeout=5)
	assert seed.returncode == 0, seed
	locks = list((root / ".kame/cache/locks").glob("*"))
	assert locks
	for path in locks:
		path.unlink()
		os.link(important, path)
	result = subprocess.run([binary, "cached"], cwd=root, capture_output=True, text=True, timeout=5)
	assert result.returncode == 0 and result.stdout == seed.stdout, result
	assert important.read_bytes() == b"keep this file\n", "cache diagnostics truncated a hard-linked project file"
	# Also guard release if a previously private lock gained an alias while held.
	with ExitStack() as stack:
		owner = start(stack, [binary, "hold"], root, "owner")
		waitFor(lambda: (root / "ready").exists(), owner)
		alias = root / "lock-alias"
		os.link(projectLock, alias)
		before = alias.read_bytes()
		assert before.startswith(b"kame-lock-v1 "), before
		(root / "release").touch()
		assert owner.wait(timeout=3) == 0
		assert alias.read_bytes() == before, "unlock truncated an inode that gained a hard link"


def nodeLocks(script: str, root: Path) -> None:
	root.mkdir()
	(root / "Makefile.kmk").write_text('task cached :\n\ttouch ready\n\twhile [ ! -f release ]; do sleep 0.05; done\n\tprintf cached\n')
	with ExitStack() as stack:
		owner = start(stack, ["node", script, "cached"], root, "owner")
		waitFor(lambda: (root / "ready").exists(), owner)
		waiter = start(stack, ["node", script, "--json", "--color=always", "cached"], root, "json")
		cancel = start(stack, ["node", script, "--output=text", "cached"], root, "cancel")
		waitFor(lambda: '"type":"lock-waiting"' in (root / "json.out").read_text(), waiter)
		waitFor(lambda: "BLOCKED" in (root / "cancel.err").read_text(), cancel)
		event = next(event for event in events(root, "json") if event["type"] == "lock-waiting")
		assert event["kind"] == "cache" and event["owner"]["pid"] == owner.pid, event
		assert str(root / ".kame/cache/locks") in event["path"], event
		assert not (root / "cancel.out").read_text()
		stop(cancel)
		assert cancel.returncode != 0 and owner.poll() is None
		terminalWait(["node", script, "--color=always", "cached"], root)
		terminalWait(["node", script, "--color=always", "cached"], root, width=40)
		terminalWait(["node", script, "--color=never", "cached"], root, color=False)
		terminalWait(["node", script, "cached"], root, color=False, environment={"NO_COLOR": "1"})
		(root / "release").touch()
		assert owner.wait(timeout=5) == 0 and waiter.wait(timeout=5) == 0
		assert any(event["type"] == "lock-acquired" for event in events(root, "json"))
		assert not (root / "json.err").read_text()
		assert not SGR.search((root / "json.out").read_bytes())
		assert not list((root / ".kame/cache/locks").iterdir())


def launcherLock(launcher: str, root: Path) -> None:
	root.mkdir()
	lock = root / "home/test-version/.lock"
	lock.mkdir(parents=True)
	pid = lock / "pid"
	pid.write_text(str(os.getpid()))
	env = {**os.environ, "KAME_HOME": str(root / "home"), "KAME_VERSION": "test-version", "KAME_NO_DOWNLOAD": "1"}
	env.pop("KAME_BIN", None)
	with ExitStack() as stack:
		waiter = start(stack, [launcher, "default"], root, "launcher", env)
		waitFor(lambda: "BLOCKED" in (root / "launcher.err").read_text(), waiter)
		log = (root / "launcher.err").read_text()
		assert str(lock) in log and f"PID {os.getpid()}" in log, log
		stop(waiter)
		assert waiter.returncode != 0 and pid.read_text() == str(os.getpid())
		assert not (root / "launcher.out").read_text()
		terminalWait([launcher, "--color=always", "default"], root, environment={**env, "KAME_BIN": ""}, dashboard=False)
		terminalWait([launcher, "--color", "never", "default"], root, color=False, environment={**env, "KAME_BIN": ""}, dashboard=False)
		terminalWait([launcher, "default"], root, color=False, environment={**env, "KAME_BIN": "", "NO_COLOR": "1"}, dashboard=False)
		# Missing owner information is explicit, not an excuse to remove the lock.
		pid.unlink()
		unknown = start(stack, [launcher, "default"], root, "unknown", env)
		waitFor(lambda: re.search(r"owner: +unavailable", (root / "unknown.err").read_text()), unknown)
		assert lock.is_dir()
		shutil.rmtree(lock)
		assert unknown.wait(timeout=3) != 0  # Downloads intentionally disabled.
		assert "lock acquired after" in (root / "unknown.err").read_text()


def main() -> None:
	binary, script = map(str, map(Path.resolve, map(Path, sys.argv[1:3])))
	launcher = str(Path(__file__).resolve().parents[1] / "bin/kame")
	with tempfile.TemporaryDirectory(prefix="kame-lock-diagnostics-", dir="/tmp/opencode") as directory:
		root = Path(directory)
		nativeLocks(binary, root / "native")
		nativeCache(binary, root / "cache")
		nativeHardlinks(binary, root / "hardlinks")
		nodeLocks(script, root / "node")
		launcherLock(launcher, root / "launcher")
	print("lock wait diagnostics: native, Node, launcher, cancellation and recovery passed")


if __name__ == "__main__":
	main()

# EOF
