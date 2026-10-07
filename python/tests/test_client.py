import asyncio
import json
import os
import tempfile
import unittest
from pathlib import Path

from kame import Kame, KameError


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
        program = await self.client.compile("answer = 42\n", name="answers.km")
        self.assertEqual(program.ast["ast"]["items"][0]["kind"], "definition")
        self.assertEqual(await program.evaluate("(count [1 2 3])"), "3")
        built = await program.build("answer")
        self.assertEqual(built["results"][0]["target"], "answer")
        self.assertEqual(built["results"][0]["value"]["data"], "42")
        self.assertTrue(any(event["type"] == "target-completed" for event in built["events"]))
        built["events"].clear()
        self.assertTrue((await program.build("answer"))["events"])

    async def test_grants_are_forwarded(self):
        (self.root / "data.txt").write_text("readable", encoding="utf-8")
        with self.assertRaises(KameError):
            await self.client.evaluate('(read "data.txt")')
        value = await self.client.evaluate('(read "data.txt")', grants={"read": ["."]})
        self.assertEqual(value, "readable")

    async def test_watch_yields_events_and_close_reaps(self):
        program = await self.client.compile("answer :\n\tprintf hi > answer\n", name="watch.kmk")
        watch = await program.watch("answer")
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
        with self.assertRaisesRegex(KameError, 'PARSE_ERR'):
            await self.client.compile('default :\nbroken = (\n', name='invalid.kmk')
        program = await self.client.compile('first :\n\t@(out "first")\nsecond :\n\t@(out "second")\n', name='rules.kmk')
        self.assertEqual(len(program.ast['ast']['items']), 2)

    async def test_watch_preserves_cwd_and_grants(self):
        (self.root / 'data.txt').write_text('watched bytes', encoding='utf-8')
        program = await self.client.compile('./output : ./data.txt\n\t@(yield (read "./data.txt"))\n', name='watch.kmk')
        watch = await program.watch('./output', grants={'read': ['.'], 'write': ['.']})
        while True:
            event = await asyncio.wait_for(watch.__anext__(), timeout=5)
            if event['type'] == 'target-completed':
                break
        self.assertEqual((self.root / 'output').read_text(), 'watched bytes')
        self.assertFalse(Path(program._tempdir, 'output').exists())
        await watch.close()
        denied = await self.client.compile('./denied :\n\t@(yield (read "./data.txt"))\n', name='denied.kmk')
        watch = await denied.watch('./denied', grants={'write': ['.']})
        while True:
            event = await asyncio.wait_for(watch.__anext__(), timeout=5)
            if event['type'] == 'target-failed':
                self.assertEqual(event['diagnostic']['code'], 'CAP_DENIED')
                break
        self.assertFalse((self.root / 'denied').exists())
        await watch.close()

    async def test_cancelled_request_reaps_process_group(self):
        task = asyncio.create_task(self.client.evaluate('(shell "sleep 10")', grants={"run": True}))
        await asyncio.sleep(0.15)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(self.client._processes)

    async def test_close_cancels_and_awaits_owned_requests(self):
        task = asyncio.create_task(self.client.evaluate('(shell "sleep 10")', grants={"run": True}))
        await asyncio.sleep(0.15)
        await self.client.close()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertFalse(self.client._processes)

    async def test_repeated_compile_and_disposal(self):
        for index in range(100):
            program = await self.client.compile(f"value = {index}\n", name=f"cycle-{index}.km")
            self.assertEqual(json.loads(await program.evaluate("value")), str(index))
            source_path = program._source_path
            await program.close()
            self.assertFalse(Path(source_path).exists())
        self.assertFalse(self.client._processes)


if __name__ == "__main__":
    unittest.main()
