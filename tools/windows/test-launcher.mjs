#!/usr/bin/env node
// Spec: docs/spec/015-distribution.md — Windows launcher runtime acceptance

import assert from 'node:assert/strict';
import { copyFile, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const bundle = path.resolve(process.argv[2] ?? path.join(here, '../../dist/windows'));
const launcher = path.join(bundle, 'kame.exe');

if (process.platform !== 'win32') {
  throw new Error('this acceptance test must run on a Windows host');
}

function run(executable, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {
      cwd: options.cwd,
      env: { ...process.env, ...options.env },
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const stdout = [];
    const stderr = [];
    let timedOut = false;
    const timeout = setTimeout(() => {
      timedOut = true;
      const killer = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], {
        stdio: 'ignore',
        windowsHide: true,
      });
      killer.unref();
    }, options.timeout ?? 30000);
    child.stdout.on('data', (chunk) => stdout.push(chunk));
    child.stderr.on('data', (chunk) => stderr.push(chunk));
    child.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.once('close', (code, signal) => {
      clearTimeout(timeout);
      resolve({
        code,
        signal,
        timedOut,
        stdout: Buffer.concat(stdout).toString('utf8'),
        stderr: Buffer.concat(stderr).toString('utf8'),
      });
    });
    child.stdin.end(options.input ?? '');
  });
}

const temporary = await mkdtemp(path.join(os.tmpdir(), 'kame-windows-launcher-'));
try {
  const version = await run(launcher, ['--version'], { cwd: temporary });
  assert.equal(version.timedOut, false, '--version timed out');
  assert.equal(version.code, 0, `--version failed: ${version.stderr}`);
  assert.match(version.stdout.trim(), /^kame \S+$/);

  const workspace = path.join(temporary, 'workspace');
  await mkdir(workspace);
  await writeFile(
    path.join(workspace, 'Windows.kmk'),
    'task windows-launcher :\r\n\techo KAME_WINDOWS_RUNTIME_OK\r\n\techo %KAME_WINDOWS_TEST%\r\n',
  );
  const build = await run(
    launcher,
    [
      '--shell', 'cmd.exe', '--shell', '/d', '--shell', '/c',
      '--env', 'KAME_WINDOWS_TEST=from-host',
      '-f', 'Windows.kmk', 'windows-launcher',
    ],
    { cwd: workspace },
  );
  assert.equal(build.timedOut, false, 'bundled CLI build timed out');
  assert.equal(build.code, 0, `bundled CLI build failed: ${build.stderr}`);
  assert.match(build.stdout, /KAME_WINDOWS_RUNTIME_OK/);
  assert.match(build.stdout, /from-host/);

  const contract = path.join(temporary, 'launcher-contract');
  await mkdir(contract);
  await copyFile(launcher, path.join(contract, 'kame.exe'));
  await writeFile(
    path.join(contract, 'kame.js'),
    `let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  process.stdout.write(JSON.stringify({ args: process.argv.slice(2), cwd: process.cwd(), env: process.env.KAME_LAUNCHER_TEST, input }));
  process.stderr.write('launcher-stderr');
  process.exitCode = 23;
});
`,
  );
  const args = ['two words', '&|;$(touch nope)', '', '雪', 'trailing\\'];
  const contractResult = await run(
    path.join(contract, 'kame.exe'),
    args,
    { cwd: contract, env: { KAME_LAUNCHER_TEST: 'available' }, input: 'launcher-stdin' },
  );
  assert.equal(contractResult.timedOut, false, 'launcher contract process timed out');
  assert.equal(contractResult.code, 23, 'launcher did not preserve the Node exit status');
  assert.equal(contractResult.stderr, 'launcher-stderr');
  assert.deepEqual(JSON.parse(contractResult.stdout), {
    args,
    cwd: contract,
    env: 'available',
    input: 'launcher-stdin',
  });

  process.stdout.write('Windows launcher runtime acceptance passed\n');
} finally {
  await rm(temporary, { recursive: true, force: true });
}
