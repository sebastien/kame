"""Measure committed input to verified idle in retained CLI watches."""

import argparse
import json
import math
import os
from pathlib import Path
import platform
import re
import signal
import subprocess
import tempfile
import time


def run(command, backend, debounce, samples, workload="input"):
	with tempfile.TemporaryDirectory(prefix="kame-watch-perf-") as tmp:
		root = Path(tmp)
		def source(i):
			return f'rule ./output = "{i}"\n'
		fixture = 'rule ./output : ./input = text (read ./input)\n'
		if workload == "reload":
			fixture = source(0)
		if workload == "cancel":
			fixture = ('rule ./output : ./input ; {shell: "sh"}\n'
				'\tif grep -q busy ./input; then touch active; exec sleep 60; fi; cp ./input ./output\n')
		(root / "Kamefile").write_text(fixture)
		(root / "input").write_text("0")
		flags = ["--cancel"] if workload == "cancel" else []
		env = {**os.environ, "KAME_WASM": str(Path("dist/kame.wasm").resolve())} if backend == "wasm" else None
		process = subprocess.Popen([*command, "--json", *flags, f"--watch={debounce}", "./output"],
			cwd=root, stdout=subprocess.PIPE, start_new_session=True, env=env)
		delays, service, totals, cancellations = [], [], [], []
		rss = {}
		try:
			for i in range(samples + 1):
				signal.alarm(30)
				expect = i + 1
				if i and workload == "cancel":
					expect = i*2 + 1
					(root / "active").unlink(missing_ok=True)
					(root / "next").write_text("busy")
					os.replace(root / "next", root / "input")
					while not (root / "active").exists():
						if process.poll() is not None:
							raise RuntimeError("watch exited before recipe started")
						time.sleep(.005)
				if i:
					(root / "next").write_text(source(i) if workload == "reload" else str(i))
				start = time.monotonic_ns()
				if i:
					os.replace(root / "next", root / ("Kamefile" if workload == "reload" else "input"))
				started, cancelledAt = None, None
				for line in process.stdout:
					now = time.monotonic_ns()
					event = json.loads(line)
					if event.get("type") == "watch-idle" and event.get("cycle") == expect-1 and event["status"] == "cancelled":
						cancelledAt = now
					if event.get("cycle") != expect:
						continue
					if event["type"] == "watch-cycle-started":
						started = now
					if event["type"] == "watch-idle":
						if i and workload == "cancel":
							if cancelledAt is None:
								raise RuntimeError("active cycle did not cancel")
							cancellations.append((cancelledAt-start)/1e6)
						if started is None or event["status"] != "success" or (root / "output").read_text() != str(i):
							raise RuntimeError(f"incorrect cycle {i+1}: {event}")
						if i:
							delays.append((started-start)/1e6)
							service.append((now-started)/1e6)
							totals.append((now-start)/1e6)
						break
				else:
					raise RuntimeError(f"watch exited at update {i}")
				signal.alarm(0)
				if i in (0, samples//2, samples):
					accounting = Path(f"/proc/{process.pid}/smaps_rollup")
					if accounting.exists():
						rss[expect] = int(re.search(r"^Rss:\s+(\d+)", accounting.read_text(), re.M)[1])
		finally:
			signal.alarm(0)
			if process.poll() is None:
				process.send_signal(signal.SIGINT)
			try:
				status = process.wait(timeout=5)
			except subprocess.TimeoutExpired:
				os.killpg(process.pid, signal.SIGKILL)
				process.wait()
				raise
		if status != 130:
			raise RuntimeError(f"{backend} watch exited with status {status}; expected 130 after SIGINT")
		result = dict(backend=backend, workload=workload, debounce_ms=debounce, samples=samples, platform=platform.platform(),
			rss_KiB_by_cycle=rss)
		startMetric = "edit_to_replacement_start" if workload == "cancel" else "edit_to_start"
		timings = [(startMetric, delays), ("start_to_idle", service), ("end_to_end", totals)]
		if cancellations:
			timings.append(("edit_to_cancelled_idle", cancellations))
		for name, values in timings:
			values.sort()
			result[name+"_ms"] = {f"p{p}": values[math.ceil(p*len(values)/100)-1]
				for p in (50, 95, 99) if p != 99 or samples >= 100}
		print(json.dumps(result), flush=True)


def timeout(signum, frame):
	raise TimeoutError("watch cycle timed out")


if __name__ == "__main__":
	parser = argparse.ArgumentParser(description=__doc__)
	parser.add_argument("--samples", type=int, default=100)
	args = parser.parse_args()
	if args.samples < 1:
		parser.error("samples must be positive")
	signal.signal(signal.SIGALRM, timeout)
	for backend, command in [("native", [str(Path("build/kame.debug").resolve())]),
			("wasm", ["node", str(Path("dist/kame.js").resolve())])]:
		for debounce in (0, 100):
			run(command, backend, debounce, args.samples)
		for workload in ("reload", "cancel"):
			run(command, backend, 100, min(args.samples, 10), workload)

# EOF
