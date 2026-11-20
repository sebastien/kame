import asyncio
import json
import os
import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

from kame import Kame, KameError, KameProgram, KameWatch


CLI = os.environ.get("KAME_TEST_CLI", "kame")


class KameClientTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="kame-python-test-")
        self.root = Path(self.directory.name)
        self.client = Kame(CLI, cwd=self.root)

    async def asyncTearDown(self):
        await self.client.close()
        self.directory.cleanup()

    async def test_compile_parse_evaluate_and_build_return_copies(self):
        program = await self.client.compile("answer = 42\n", name="answers.kame")
        self.assertEqual(program.ast["ast"]["items"][0]["kind"], "definition")
        self.assertEqual(await program.evaluate("count [1, 2, 3]"), "3")
        built = await program.build("answer")
        self.assertEqual(built["results"][0]["target"], "answer")
        self.assertEqual(built["results"][0]["value"]["data"], 42)
        self.assertTrue(any(event["type"] == "target-completed" for event in built["events"]))
        built["events"].clear()
        self.assertTrue((await program.build("answer"))["events"])

    async def test_build_forwards_job_limit(self):
        program = await self.client.compile('task answer ; {shell: kash}\n\t(nop nil)\n', name="jobs.km")
        run = program._run
        invocation = []

        async def capture(args, **kwargs):
            invocation.extend(args)
            return await run(args, **kwargs)

        with patch.object(program, "_run", capture):
            built = await program.build("answer", jobs=2)
        self.assertEqual(invocation[2:4], ["--jobs", "2"])
        self.assertTrue(any(event["type"] == "target-completed" for event in built["events"]))

    async def test_grants_are_forwarded(self):
        (self.root / "data.txt").write_text("readable", encoding="utf-8")
        with self.assertRaises(KameError):
            await self.client.evaluate('(read "data.txt")')
        value = await self.client.evaluate('(read "data.txt")', grants={"read": ["."]})
        self.assertEqual(value, "readable")

    async def test_direct_kash_parse_and_format_preserve_expression_boundaries(self):
        source = 'printf "%s" (str "two " "words")\n(nop {value: 1})\n'
        sourcePath = self.root / 'direct.kash'
        sourcePath.write_text(source, encoding='utf-8')
        formatted = await self.client._run(['do', 'fmt', '--lang', 'kash', str(sourcePath)])
        for text in (source, formatted):
            program = await self.client.compile(text, name='direct.kash')
            items = program.ast['ast']['items']
            self.assertEqual([item['kind'] for item in items], ['command', 'expression'])
            argument = items[0]['expression']['items'][0]['items'][2]
            self.assertEqual(argument['kind'], 'command-word')
            self.assertEqual(argument['parts'][0]['form'], '(')
            self.assertEqual(argument['parts'][0]['expression']['kind'], 'group')
            expression = argument['parts'][0]['expression']['expression']
            self.assertEqual(expression['kind'], 'application')
            self.assertEqual(expression['items'][0]['text'], 'str')
            self.assertEqual(items[1]['expression']['expression']['items'][1]['kind'], 'record')
            await program.close()

    async def test_typed_expression_argv_is_not_template_reparsed(self):
        program = await self.client.compile(
            'rule ./argv.txt\n'
            '\tlet argv = concat [(str """@(missing)""" " two words")] ["*.c", """$(touch injected)""", ""]\n'
            '\tprintf "<%s>" $...argv > ./argv.txt\n'
            '\t(nop {value: 1})\n', name='typed-argv.km',
        )
        await program.build('./argv.txt', grants={'run': True, 'write': ['.']})
        self.assertEqual((self.root / 'argv.txt').read_text(), '<@(missing) two words><*.c><$(touch injected)><>')
        self.assertFalse((self.root / 'injected').exists())

    async def test_scalar_str_and_explicit_json_round_trip(self):
        for collection in ('[1, 2]', '{value: [1, 2]}'):
            with self.assertRaisesRegex(KameError, 'EXPR_INVALID'):
                await self.client.evaluate(f'(str {collection})')
            self.assertEqual(await self.client.evaluate(f'(parse-json (json {collection})) == {collection}'), 'true')
        self.assertEqual(json.loads(json.loads(await self.client.evaluate('json {value: [1, 2]}'))), {'value': [1, 2]})

    async def test_canonical_action_effects_and_authored_failure_are_not_display_or_recovery(self):
        self.assertEqual(await self.client.evaluate('{value: 1}'), '{value: 1}')
        self.assertEqual(await self.client.evaluate('out! "out"'), 'out"out"')
        communicate = self.client._communicate
        captured = []

        async def capture(*args, **kwargs):
            result = await communicate(*args, **kwargs)
            captured.append(result)
            return result

        with patch.object(self.client, '_communicate', capture):
            self.assertEqual(await self.client.evaluate('(err! "err")'), '"err"')
        self.assertEqual(captured[0][1], b'"err"')
        self.assertEqual(captured[0][2], b'err')
        with self.assertRaisesRegex(KameError, 'USER_ERROR'):
            await self.client.evaluate('(fail! "stop")')
        program = await self.client.compile(
            'let value = (fail! "stop") ?? (nop "fallback")\n(out! value)\n', name='failure.kash',
        )
        with self.assertRaisesRegex(KameError, 'USER_ERROR'):
            await program._run(['do', 'run', '--lang', 'kash', '-f', program._source_path])

    async def test_watch_yields_events_and_close_reaps(self):
        program = await self.client.compile("task answer\n", name="watch.km")
        start = program._start
        invocation = []

        async def capture(argv, **kwargs):
            invocation.extend(argv)
            return await start(argv, **kwargs)

        with patch.object(program, "_start", capture):
            watch = await program.watch("answer", jobs=2, debounce_ms=200, cancel_on_change=True)
        self.assertEqual(invocation[1:5], ["--jobs", "2", "--watch=200", "--cancel"])
        events = []
        while not any(event["type"] == "watch-idle" for event in events):
            events.append(await asyncio.wait_for(watch.__anext__(), timeout=5))
        self.assertTrue(any(event["type"] == "target-started" for event in events))
        self.assertTrue(any(event["type"] == "target-completed" for event in events))
        pending = asyncio.create_task(watch.__anext__())
        await asyncio.sleep(0.02)
        await watch.close()
        with self.assertRaises(asyncio.CancelledError):
            await pending
        self.assertFalse(program._processes)

    async def test_rule_compile_parses_the_entire_source(self):
        for options in ({"name": "legacy.kmk"}, {"language": "kmk"}):
            with self.assertRaises(ValueError):
                await self.client.compile("task default\n", **options)
        with self.assertRaisesRegex(KameError, 'PARSE_ERR'):
            await self.client.compile('(count [1, 2])\n', name='Kamefile')
        with self.assertRaisesRegex(KameError, 'PARSE_ERR'):
            await self.client.compile('task default\nbroken = (\n', name='invalid.km')
        program = await self.client.compile('task first ; {shell: kash}\n\t(out! "first")\ntask second ; {shell: kash}\n\t(out! "second")\n', name='rules.km')
        self.assertEqual(len(program.ast['ast']['items']), 2)

    async def test_watch_preserves_cwd_and_grants(self):
        (self.root / 'data.txt').write_text('watched bytes', encoding='utf-8')
        program = await self.client.compile('rule ./output : ./data.txt = read "./data.txt"\n', name='watch.km')
        watch = await program.watch('./output', grants={'read': ['.'], 'write': ['.']})
        while True:
            event = await asyncio.wait_for(watch.__anext__(), timeout=5)
            if event['type'] == 'target-completed':
                break
        self.assertEqual((self.root / 'output').read_text(), 'watched bytes')
        self.assertFalse(Path(program._tempdir, 'output').exists())
        await watch.close()
        denied = await self.client.compile('rule ./denied = read "./data.txt"\n', name='denied.km')
        watch = await denied.watch('./denied', grants={'write': ['.']})
        while True:
            event = await asyncio.wait_for(watch.__anext__(), timeout=5)
            if event['type'] == 'target-failed':
                self.assertEqual(event['diagnostic']['code'], 'CAP_DENIED')
                break
        self.assertFalse((self.root / 'denied').exists())
        await watch.close()

    async def test_cancelled_request_reaps_process_group(self):
        task = asyncio.create_task(self.client.evaluate('(sh! "sleep 10")', grants={"run": True}))
        await asyncio.sleep(0.15)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(self.client._processes)

    async def test_close_cancels_and_awaits_owned_requests(self):
        task = asyncio.create_task(self.client.evaluate('(sh! "sleep 10")', grants={"run": True}))
        await asyncio.sleep(0.15)
        await self.client.close()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(self.client._processes)

    async def test_repeated_compile_and_disposal(self):
        for index in range(100):
            program = await self.client.compile(f"value = {index}\n", name=f"cycle-{index}.km")
            self.assertEqual(json.loads(await program.evaluate("value")), index)
            source_path = program._source_path
            await program.close()
            self.assertFalse(Path(source_path).exists())
        self.assertFalse(self.client._processes)

    async def test_close_and_cancel_during_spawn_await_reaping(self):
        original = asyncio.create_subprocess_exec
        for operation in ("evaluate", "compile", "program", "watch", "cancel"):
            with self.subTest(operation=operation):
                client = Kame(CLI, cwd=self.root)
                program = await client.compile("task answer\n", name="watch.km")
                entered, release = asyncio.Event(), asyncio.Event()
                children = []

                async def delayed(*args, **kwargs):
                    entered.set()
                    await release.wait()
                    child = await original(sys.executable, "-c", "import time; time.sleep(30)", **kwargs)
                    children.append(child)
                    return child

                with patch("asyncio.create_subprocess_exec", delayed):
                    request = asyncio.create_task(
                        client.compile("x = 1\n") if operation == "compile" else
                        program.watch("answer") if operation == "watch" else
                        program.build("answer") if operation == "program" else
                        client.evaluate("1")
                    )
                    await asyncio.wait_for(entered.wait(), 2)
                    if operation == "cancel":
                        request.cancel()
                        closing = second = None
                    else:
                        owner = program if operation in ("program", "watch") else client
                        closing = asyncio.create_task(owner.close())
                        second = asyncio.create_task(owner.close())
                    await asyncio.sleep(0.02)
                    self.assertFalse(request.done())
                    if operation == "cancel":
                        request.cancel()
                    if closing is not None:
                        self.assertFalse(closing.done())
                        self.assertFalse(second.done(), "second close returned before spawn cleanup")
                    release.set()
                    with self.assertRaises(asyncio.CancelledError):
                        await asyncio.wait_for(request, 3)
                    if closing is not None:
                        await asyncio.wait_for(asyncio.gather(closing, second), 3)
                    self.assertTrue(children)
                    self.assertTrue(all(child.returncode is not None for child in children))
                    self.assertFalse(client._processes)
                    self.assertFalse(program._processes)
                await client.close()

    async def test_watch_cancelled_reader_closes_without_self_wait(self):
        program = await self.client.compile("task answer\n", name="watch.km")
        watch = await program.watch("answer")
        while (await asyncio.wait_for(watch.__anext__(), 3))["type"] != "watch-idle":
            pass
        pending = asyncio.create_task(watch.__anext__())
        await asyncio.sleep(0.02)
        pending.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await asyncio.wait_for(pending, 3)
        await asyncio.wait_for(watch.close(), 3)
        self.assertFalse(program._processes)

    async def test_watch_drains_stderr_and_close_has_shared_barrier(self):
        program = KameProgram(self.client, "", "", "km", "watch.km")
        process = await program._start(
            [sys.executable, "-c", 'import sys, time; sys.stderr.write("x" * 1000000); sys.stderr.flush(); print("{}", flush=True); time.sleep(30)'],
            cwd=self.client.cwd, env=None,
        )
        watch = KameWatch(program, process)
        program._watches.add(watch)
        try:
            self.assertEqual(await asyncio.wait_for(watch.__anext__(), 3), {})
            self.assertLessEqual(len(watch._stderr), 65536)
            pending = asyncio.create_task(watch.__anext__())
            await asyncio.sleep(0.02)
            entered, release = asyncio.Event(), asyncio.Event()
            stop = program._stop

            async def delayed_stop(*args, **kwargs):
                entered.set()
                await release.wait()
                await stop(*args, **kwargs)

            with patch.object(program, "_stop", delayed_stop):
                first = asyncio.create_task(watch.close())
                await asyncio.wait_for(entered.wait(), 3)
                second = asyncio.create_task(watch.close())
                await asyncio.sleep(0.02)
                second_done = second.done()
                release.set()
                await asyncio.wait_for(asyncio.gather(first, second), 3)
                self.assertFalse(second_done, "second watch close returned before reaping")
            with self.assertRaises(asyncio.CancelledError):
                await pending
            self.assertIsNotNone(process.returncode)
            self.assertTrue(watch._stderr_task.done())
            self.assertFalse(program._processes)
        finally:
            await watch.close()


if __name__ == "__main__":
    unittest.main()
