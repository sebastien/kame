"""Asynchronous embedding client for the Kame command line."""

from __future__ import annotations

import asyncio
import json
import os
import shutil
import signal
import tempfile
from pathlib import Path
from typing import Any, AsyncIterator, Iterable, Mapping


class KameError(RuntimeError):
    def __init__(self, message: str, *, returncode: int | None = None, stderr: str = "") -> None:
        super().__init__(message)
        self.returncode = returncode
        self.stderr = stderr


class _ProcessOwner:
    def __init__(self) -> None:
        self._processes: set[asyncio.subprocess.Process] = set()
        self._requests: set[asyncio.Task[Any]] = set()

    async def _start(self, argv: list[str], *, cwd: str, env: Mapping[str, str] | None) -> asyncio.subprocess.Process:
        process = await asyncio.create_subprocess_exec(
            *argv,
            cwd=cwd,
            env=dict(env) if env is not None else None,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            start_new_session=(os.name == "posix"),
        )
        self._processes.add(process)
        return process

    def _forget(self, process: asyncio.subprocess.Process) -> None:
        self._processes.discard(process)

    async def _stop(self, process: asyncio.subprocess.Process) -> None:
        if process.returncode is None:
            try:
                if os.name == "posix":
                    os.killpg(process.pid, signal.SIGTERM)
                else:
                    process.terminate()
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(process.wait(), timeout=0.5)
            except asyncio.TimeoutError:
                try:
                    if os.name == "posix":
                        os.killpg(process.pid, signal.SIGKILL)
                    else:
                        process.kill()
                except ProcessLookupError:
                    pass
                await process.wait()
        if process.stdout is not None or process.stderr is not None:
            await process.communicate()
        self._forget(process)

    async def _close_processes(self) -> None:
        current = asyncio.current_task()
        requests = tuple(task for task in self._requests if task is not current)
        for task in requests:
            task.cancel()
        if requests:
            await asyncio.gather(*requests, return_exceptions=True)
        await asyncio.gather(*(self._stop(process) for process in tuple(self._processes)), return_exceptions=True)


def _grant_args(grants: Mapping[str, Iterable[str] | bool] | None) -> list[str]:
    result: list[str] = []
    for capability, value in (grants or {}).items():
        if capability not in {"read", "write", "run", "env"}:
            raise ValueError(f"unknown capability: {capability}")
        option = f"--allow-{capability}"
        if value is True:
            result.append(option)
        elif value is False or value is None:
            continue
        elif isinstance(value, str):
            result.append(f"{option}={value}")
        else:
            result.extend(f"{option}={name}" for name in value)
    return result


class Kame(_ProcessOwner):
    """Owns Kame requests and programs; use with ``async with``."""

    def __init__(self, executable: str | os.PathLike[str] = "kame", *, cwd: str | os.PathLike[str] = ".", env: Mapping[str, str] | None = None, grants: Mapping[str, Iterable[str] | bool] | None = None) -> None:
        super().__init__()
        self.executable = os.fspath(executable)
        self.cwd = os.path.abspath(os.fspath(cwd))
        self.env = dict(env) if env is not None else None
        self.grants = dict(grants or {})
        self._programs: set[KameProgram] = set()
        self._closed = False

    async def __aenter__(self) -> Kame:
        self._check_open()
        return self

    async def __aexit__(self, exc_type: Any, exc: Any, tb: Any) -> None:
        await self.close()

    def _check_open(self) -> None:
        if self._closed:
            raise KameError("Kame client is closed")

    async def compile(self, source: str, *, name: str = "embedded.km", language: str | None = None) -> KameProgram:
        self._check_open()
        suffix = Path(name).suffix or ".km"
        run_lang = language or {".km": "km", ".kmk": "kmk", ".kash": "kash", ".ksh": "kash"}.get(suffix)
        if run_lang is None:
            raise ValueError(f"cannot infer Kame language from {name!r}")
        parse_lang = {"km": "script", "kmk": "rule", "kash": "kash"}.get(run_lang, run_lang)
        tempdir = tempfile.mkdtemp(prefix="kame-embed-")
        source_filename = "Makefile.kmk" if run_lang == "kmk" else (os.path.basename(name) or f"source{suffix}")
        source_path = os.path.join(tempdir, source_filename)
        Path(source_path).write_text(source, encoding="utf-8")
        program = KameProgram(self, source_path, tempdir, run_lang, name)
        try:
            parse = await self._run(["do", "parse", "--lang", parse_lang, source_path], grants={})
            program.ast = json.loads(parse)
        except BaseException:
            shutil.rmtree(tempdir, ignore_errors=True)
            raise
        self._programs.add(program)
        return program

    async def evaluate(self, expression: str, *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> str:
        self._check_open()
        argv = ["do", "run", "--lang", "expr", "-c", expression]
        return await self._run(argv, grants=grants)

    async def _run(self, args: list[str], *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> str:
        argv = [self.executable, *args[:2], *_grant_args(self.grants if grants is None else grants), *args[2:]] if args[:2] == ["do", "run"] else [self.executable, *args]
        process = await self._start(argv, cwd=self.cwd, env=self.env)
        task = asyncio.current_task()
        if task is not None:
            self._requests.add(task)
        try:
            stdout, stderr = await process.communicate()
        except asyncio.CancelledError:
            await self._stop(process)
            raise
        finally:
            if task is not None:
                self._requests.discard(task)
            self._forget(process)
        if process.returncode:
            message = stderr.decode("utf-8", "replace").strip() or f"Kame exited with status {process.returncode}"
            raise KameError(message, returncode=process.returncode, stderr=stderr.decode("utf-8", "replace"))
        return stdout.decode("utf-8", "replace").rstrip("\n")

    async def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        await asyncio.gather(*(program.close() for program in tuple(self._programs)), return_exceptions=True)
        await self._close_processes()


class KameProgram(_ProcessOwner):
    def __init__(self, owner: Kame, source_path: str, tempdir: str, language: str, name: str) -> None:
        super().__init__()
        self._owner = owner
        self._source_path = source_path
        self._tempdir = tempdir
        self.language = language
        self.name = name
        self.ast: dict[str, Any] = {}
        self._closed = False
        self._watches: set[KameWatch] = set()

    def _check_open(self) -> None:
        self._owner._check_open()
        if self._closed:
            raise KameError("Kame program is closed")

    async def _run(self, args: list[str], *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> str:
        self._check_open()
        argv = [self._owner.executable, *args[:2], *_grant_args(self._owner.grants if grants is None else grants), *args[2:]]
        process = await self._start(argv, cwd=self._owner.cwd, env=self._owner.env)
        task = asyncio.current_task()
        if task is not None:
            self._requests.add(task)
        try:
            stdout, stderr = await process.communicate()
        except asyncio.CancelledError:
            await self._stop(process)
            raise
        finally:
            if task is not None:
                self._requests.discard(task)
            self._forget(process)
        if process.returncode:
            message = stderr.decode("utf-8", "replace").strip() or f"Kame exited with status {process.returncode}"
            raise KameError(message, returncode=process.returncode, stderr=stderr.decode("utf-8", "replace"))
        return stdout.decode("utf-8", "replace").rstrip("\n")

    async def evaluate(self, expression: str, *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> str:
        self._check_open()
        if self.language != "km":
            raise KameError("expression evaluation is supported for .km programs")
        return await self._run(
            ["do", "run", "--lang", "km", "-f", self._source_path, "-c", expression],
            grants=grants,
        )

    async def build(self, targets: str | Iterable[str], *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> dict[str, Any]:
        self._check_open()
        selected = [targets] if isinstance(targets, str) else list(targets)
        args = ["do", "run", "--json", "--lang", self.language, "-f", self._source_path, *selected]
        output = await self._run(args, grants=grants)
        events = [json.loads(line) for line in output.splitlines() if line.strip()]
        results = [{"target": target, "value": next((event.get("value") for event in reversed(events) if event.get("target") == target and "value" in event), None)} for target in selected]
        return {"results": results, "events": events}

    async def watch(self, targets: str | Iterable[str], *, grants: Mapping[str, Iterable[str] | bool] | None = None) -> KameWatch:
        self._check_open()
        if self.language != "kmk":
            raise KameError("watch requires a .kmk rule program")
        selected = [targets] if isinstance(targets, str) else list(targets)
        args = ["--watch", "--json", *selected]
        argv = [self._owner.executable, *_grant_args(self._owner.grants if grants is None else grants), *args]
        process = await self._start(argv, cwd=self._tempdir, env=self._owner.env)
        watch = KameWatch(self, process)
        self._watches.add(watch)
        return watch

    async def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        await asyncio.gather(*(watch.close() for watch in tuple(self._watches)), return_exceptions=True)
        await self._close_processes()
        shutil.rmtree(self._tempdir, ignore_errors=True)
        self._owner._programs.discard(self)


class KameWatch(AsyncIterator[dict[str, Any]]):
    def __init__(self, program: KameProgram, process: asyncio.subprocess.Process) -> None:
        self._program = program
        self._process = process
        self._closed = False
        self._reader_task: asyncio.Task[Any] | None = None

    def __aiter__(self) -> KameWatch:
        return self

    async def __anext__(self) -> dict[str, Any]:
        if self._closed or self._process.stdout is None:
            raise StopAsyncIteration
        self._reader_task = asyncio.current_task()
        try:
            line = await self._process.stdout.readline()
        except asyncio.CancelledError:
            await self.close()
            raise
        finally:
            self._reader_task = None
        if not line:
            stderr = await self._process.stderr.read() if self._process.stderr else b""
            status = await self._process.wait()
            self._program._forget(self._process)
            self._closed = True
            self._program._watches.discard(self)
            if status:
                raise KameError(stderr.decode("utf-8", "replace").strip() or f"Kame watch exited with status {status}", returncode=status)
            raise StopAsyncIteration
        return json.loads(line)

    async def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        reader = self._reader_task
        if reader is not None and reader is not asyncio.current_task():
            reader.cancel()
            await asyncio.gather(reader, return_exceptions=True)
        await self._program._stop(self._process)
        self._program._watches.discard(self)


__all__ = ["Kame", "KameProgram", "KameWatch", "KameError"]
