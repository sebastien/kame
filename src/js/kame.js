#!/usr/bin/env node
// Kame JavaScript CLI: the WebAssembly counterpart of the native CLI in
// docs/spec/009-cli.md. One ESM file, Node 18+, no third-party dependencies,
// an adjacent kame.wasm. See docs/spec/010-wasm.md.
//
// The command grammar (options, defaults, arity, conflicts, usage diagnostics)
// is parsed by the shared portable `cli` package inside the module via
// kame_wasm_cli, so the native CLI and this wrapper accept the same words. This
// file supplies only host capabilities and stream/exit policy.
import { lstat, mkdir, mkdtemp, open, readFile, readlink, rename, rm, stat } from 'node:fs/promises';
import { accessSync, closeSync, constants as fsConstants, createWriteStream, existsSync, fstatSync, ftruncateSync, lstatSync, mkdtempSync, mkdirSync, openSync, readFileSync, readlinkSync, readSync, readdirSync, realpathSync, rmSync, rmdirSync, statSync, unlinkSync, watch as fsWatch, writeFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, isAbsolute, join, normalize, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { constants as osConstants, tmpdir } from 'node:os';
import { Socket } from 'node:net';
import process, { argv, env, stderr, stdout } from 'node:process';

// Numeric graph kinds are part of the stable WASM ABI.
const graphKinds = Object.freeze({ inputs: 0, outputs: 1, span: 2, planRoots: 3, list: 4, listReport: 5 });

// Children run in their own process group so a signal terminates the whole
// tree, and a forced termination reports 128 plus the signal number.
const activeChildren = new Set();
const cleanupChildren = new WeakSet();
const invocationCancellations = new Set();
const invocationDisposals = new Set();
const activeCacheLocks = new Set();
const memoryResourceFiles = new Map();
const memoryResourceDirectories = new Set();
let memoryResourceClock = 0n;
let interruptedStatus = 0;

function installSignals() {
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      if (interruptedStatus) return;
      clearDashboard();
      interruptedStatus = 128 + (osConstants.signals[signal] ?? 0);
      syncOutputReaders();
      const reaped = [...activeChildren].map((child) => new Promise((resolve) => child.once('close', resolve)));
      for (const cancellation of invocationCancellations) cancellation.abort();
      const cacheReleases = [...activeCacheLocks].map((release) => release());
      const treeStops = [...activeChildren].filter(child => !cleanupChildren.has(child)).map((child) => signalProcessTree(child, 'SIGKILL'));
      Promise.all([...reaped, ...treeStops, ...invocationDisposals, ...cacheReleases]).then(() => process.exit(interruptedStatus));
    });
  }
}

function track(child, group = undefined, finalizing = false) {
  activeChildren.add(child);
  if (finalizing) cleanupChildren.add(child);
  group?.add(child);
  child.once('close', () => {
    activeChildren.delete(child);
    group?.delete(child);
  });
}

function signalProcessTree(child, signal = 'SIGKILL') {
  if (!child || child.pid === undefined) return Promise.resolve();
  if (process.platform === 'win32') {
    const killer = spawn('taskkill.exe', ['/PID', `${child.pid}`, '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    return new Promise((resolve) => {
      killer.once('error', () => { try { child.kill(signal); } catch {} resolve(); });
      killer.once('close', resolve);
    });
  }
  try { process.kill(-child.pid, signal); } catch { try { child.kill(signal); } catch {} }
  return Promise.resolve();
}

async function cancelProcessGroup(group, graceMS = 5000) {
  const children = [...(group ?? [])];
  const reaped = children.map((child) => new Promise((resolve) => child.once('close', resolve)));
  let timer;
  try {
    await Promise.all(children.map((child) => signalProcessTree(child, process.platform === 'win32' ? 'SIGKILL' : 'SIGTERM')));
    timer = setTimeout(() => { for (const child of children) if (group.has(child)) void signalProcessTree(child, 'SIGKILL'); }, graceMS);
    timer.unref();
    await Promise.all(reaped);
  } finally { clearTimeout(timer); }
}

// A blocked public sink must stop every publishing child pipe. One pair of
// drain listeners serves concurrent children without accumulating listeners.
const publishedReaders = new Set();
const publicationWaiters = new Set();
function syncOutputReaders() {
  const blocked = stdout.writableNeedDrain || stderr.writableNeedDrain;
  if (!blocked || interruptedStatus) for (const settle of publicationWaiters) settle();
  for (const reader of publishedReaders) {
    if (blocked && !interruptedStatus && !reader.stopped()) reader.stream.pause();
    else reader.stream.resume();
  }
}
function waitOutputReady(signal) {
  if ((!stdout.writableNeedDrain && !stderr.writableNeedDrain) || interruptedStatus || signal?.aborted) return Promise.resolve();
  return new Promise((resolveReady) => {
    const settle = () => {
      publicationWaiters.delete(settle);
      signal?.removeEventListener('abort', settle);
      resolveReady();
    };
    publicationWaiters.add(settle);
    signal?.addEventListener('abort', settle, { once: true });
  });
}
stdout.on('drain', syncOutputReaders);
stderr.on('drain', syncOutputReaders);
function publishReader(stream, publish, stopped) {
  const reader = { stream, stopped };
  publishedReaders.add(reader);
  stream.on('data', (chunk) => { publish(chunk); syncOutputReaders(); });
  stream.once('close', () => publishedReaders.delete(reader));
  syncOutputReaders();
}

const STAGE = 2;
const here = dirname(fileURLToPath(import.meta.url));
const wasmPath = env.KAME_WASM ? resolve(env.KAME_WASM) : resolve(here, 'kame.wasm');

function version() {
  if (env.KAME_VERSION) return env.KAME_VERSION;
  for (const candidate of [resolve(here, 'VERSION'), resolve(here, '..', 'VERSION')]) {
    try {
      return readFileSync(candidate, 'utf8').trim();
    } catch {
      // try the next location
    }
  }
  return 'unknown';
}

function versionIdentity() {
  try {
    let metadata;
    for (const candidate of [join(here, 'PROVENANCE.json'), join(here, '..', 'PROVENANCE.json')]) {
      try { lstatSync(candidate); }
      catch (error) { if (error.code === 'ENOENT') continue; return null; }
      if (!statSync(candidate).isFile()) return null;
      metadata = JSON.parse(readFileSync(candidate, 'utf8'));
      break;
    }
    if (metadata?.schema !== 1 || typeof metadata.version !== 'string' ||
        !/^[A-Za-z0-9.+-]+$/.test(metadata.version) || metadata.version === 'latest' ||
        typeof metadata.sourceRevision !== 'string' || !metadata.sourceRevision.trim() || metadata.sourceRevision === 'unknown' ||
        typeof metadata.created !== 'string' || !Number.isFinite(Date.parse(metadata.created)) || !Array.isArray(metadata.subjects)) return null;
    const subjects = new Map();
    for (const subject of metadata.subjects) {
      if (typeof subject?.name !== 'string' || !subject.name || typeof subject.sha256 !== 'string' ||
          !/^[0-9a-f]{64}$/.test(subject.sha256) || subjects.has(subject.name)) return null;
      subjects.set(subject.name, subject.sha256);
    }
    for (const [name, path] of [['kame.js', fileURLToPath(import.meta.url)], ['kame.wasm', wasmPath]]) {
      if (!subjects.has(name) || !statSync(path).isFile() ||
          createHash('sha256').update(readFileSync(path)).digest('hex') !== subjects.get(name)) return null;
    }
    return { version: metadata.version, buildID: metadata.sourceRevision };
  } catch {
    // Local byte correspondence is optional reporting, never authentication.
    return null;
  }
}

let jsonMode = false;
let outputMode = 'ansi';
let outputColor = false;
let presentationCommand = 'build';
let presentationSubject = '';
let invocationHadDiagnostic = false;
let cliWorkers = [];
let dashboardRows = 0;
let dashboardWidth = 0, dashboardHeight = 0;
let dashboardUnsafe = false;
let dashboardPending = false;
let dashboardLast = 0;
let terminalOutcomes = new Set();
let startedOutcomes = new Set();
let formatCounts = { completed: 0, failed: 0, cancelled: 0 };
let watchIdle = false;
const cliLockWaits = new Map();
let watchCycle = 0, watchCycleStatus = '', watchCycleElapsed = 0;
// lastDiagnostic holds the rich diagnostic from the most recent failed target
// event, so the trailing --json diagnostic matches the native duplicate.
let lastDiagnostic = null;

// Presentation state for the primary invocation's human output. The wrapper
// mirrors the native CLI's progress, stream forwarding, diagnostic rendering,
// and summary so non-JSON runs read the same on both backends.
let diagnosticFormat = 'plain';
let diagnosticColor = false;
let buildProgress = null;
let buildStartedAt = 0;
let primarySource = null;
const sourceTexts = new Map();

function diagnostic(code, message) {
  invocationHadDiagnostic = true;
  clearDashboard();
  const detail = lastDiagnostic ?? { code, severity: 'error', message };
  if (jsonMode) {
    stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
    return;
  }
  stderr.write(renderDiagnostic(detail, primarySource, 80));
}

// resolveColor mirrors the native CLI: color only applies to the human format
// and respects NO_COLOR, CLICOLOR, CLICOLOR_FORCE, TERM, and a TTY check.
function resolveColor(color, format, destination = stderr) {
  if (format !== 'human' || color === 'never') return false;
  if (color === 'always') return true;
  if (env.NO_COLOR || env.CLICOLOR === '0' || env.TERM === 'dumb') return false;
  if (env.CLICOLOR_FORCE && env.CLICOLOR_FORCE !== '0') return true;
  return destination.isTTY === true;
}

const semanticStyles = {
  'target.rule': '33', 'target.service': '36', 'target.task': '35', 'target.artifact': '34',
  heading: '1', 'value.target': '1', 'value.symbol': '1', 'value.tool': '1', 'value.option': '1', 'diagnostic.code': '1',
  'value.path': '36', location: '36', 'message.info': '36', 'status.running': '36', 'progress.complete': '36',
  'message.tip': '1;36', 'message.warning': '93', 'status.retrying': '93', 'status.cancelled': '33',
  'message.error': '1;31', 'message.fatal': '1;31', 'status.failed': '1;31',
  'status.success': '32', 'status.reused': '32', 'status.ready': '32', 'text.muted': '2', structure: '2',
};
function styled(token, text, enabled = diagnosticColor) {
  const code = enabled && semanticStyles[token];
  return code ? `\x1b[${code}m${text}\x1b[0m` : text;
}
function configurePresentation(inv) {
  outputMode = inv.output || 'ansi';
  jsonMode = outputMode === 'json' || inv.json === true;
  diagnosticFormat = inv.diagnosticFormat || (outputMode === 'ansi' ? 'human' : 'plain');
  diagnosticColor = resolveColor(inv.color, 'human');
  outputColor = resolveColor(inv.color, 'human', stdout);
}
function writeReport(heading, document) {
  const data = typeof document === 'string' || Buffer.isBuffer(document) || document instanceof Uint8Array ? JSON.parse(Buffer.from(document).toString('utf8')) : document;
  if (jsonMode) { stdout.write(`${JSON.stringify(data)}\n`); return; }
  stdout.write(`${styled('heading', heading, outputColor)}\n`);
  const write = (value, label = '', depth = 1, root = false) => {
    if (value && typeof value === 'object') {
      if (label) { stdout.write(`${'  '.repeat(depth)}${styled('value.symbol', label, outputColor)}\n`); depth++; }
      if (Array.isArray(value)) {
        if (!value.length) stdout.write(`${'  '.repeat(depth)}${depth === 1 ? heading === 'tools' ? 'no referenced tools' : heading === 'cache list' ? 'no managed records' : heading.startsWith('inputs ') ? 'no inputs' : heading.startsWith('outputs ') ? 'no outputs' : 'no entries' : '(none)'}\n`);
        for (const item of value) write(item, '', depth);
      } else for (const [key, item] of Object.entries(value)) { if (!root || (key !== 'schema' && key !== 'type')) write(item, key, depth); }
      return;
    }
    const plain = ['', 'source', 'path', 'target', 'name', 'kind', 'freshness', 'backend', 'key'].includes(label) && value !== '' && !/[\s\x00-\x1f\x7f]/u.test(value);
    let text = value === null ? ':nil' : typeof value === 'boolean' ? value ? ':true' : ':false' : typeof value === 'string' ? plain ? value : JSON.stringify(value) : String(value);
    if (label === 'path' && value === '') text = 'unavailable';
    stdout.write(`${'  '.repeat(depth)}${label ? `${styled('value.symbol', label, outputColor)}: ` : ''}${styled(['source', 'path', 'target', ''].includes(label) ? 'value.path' : 'value.literal', text, outputColor)}\n`);
  };
  if (data.type === 'list') writeTargetReport(data);
  else if (data.schema === 2) writeInspectionReport(data, write);
  else write(data, '', 1, true);
}

function writeTargetReport(doc) {
  if (!doc.entries.length) stdout.write('  no entries\n');
  for (const [group, heading, token] of [['rules', 'Rules', 'target.rule'], ['services', 'Services', 'target.service'], ['tasks', 'Tasks', 'target.task'], ['artifacts', 'Artifacts', 'target.artifact']]) {
    const entries = doc.entries.filter((entry) => entry.group === group);
    if (!entries.length) continue;
    stdout.write(`  ${styled(token, heading, outputColor)}\n`);
    for (const entry of entries) {
      const [label, statusToken] = entry.status === 'built' ? ['ok', 'status.success'] : entry.status === 'outdated' ? ['out of date', 'message.warning'] : [entry.status, entry.status === 'missing' ? 'status.failed' : 'text.muted'];
      stdout.write(`    ${styled(token, shortReportField(entry.name), outputColor)}${label ? `  ${styled(statusToken, label, outputColor)}` : ''}\n`);
    }
  }
}

function writeInspectionReport(doc, write) {
  const resources = doc.resources, producers = doc.producers;
  const downstream = doc.direction === 'downstream';
  const producer = (id) => producers.find((p) => p.id === id);
  const text = (value, token = 'value.path') => styled(token, shortReportField(value), outputColor);
  stdout.write('  scope: declared and read-only resolved\n');
  if (downstream) stdout.write('  direction: downstream · potentially affected, not guaranteed to rebuild\n');
  for (let section = 0; section < 3; section++) {
    const label = section === 1 ? 'configuration' : section === 2 ? 'logical / value / service resources' : doc.type === 'outputs' ? 'artifacts' : doc.type === 'plan' ? 'files' : 'file inputs';
    stdout.write(`  ${styled('heading', label, outputColor)}\n`);
    let count = 0;
    if (doc.depth !== 0) for (const r of resources) {
      const file = r.key.kind === 'file', config = r.roles.includes('configuration');
      const include = section === 1 ? config : section === 2 ? !file && !config : file && !config && (doc.type === 'plan' || r.roles.includes(doc.type === 'outputs' ? 'artifact' : 'input'));
      if (!include) continue;
      count++;
      const references = new Set(doc.dependencies.filter((d) => d.resource === r.id).map((d) => d.producer));
      if (producer(r.producer)) references.add(r.producer);
      stdout.write(`    ${text(r.display)} [${references.size}]${r.status ? ` · ${r.status}` : ''}\n`);
    }
    if (!count) stdout.write(`    ${section === 0 && doc.truncated ? 'not expanded at requested depth' : section === 0 && doc.deferred.length ? 'unresolved discovery; see boundaries below' : section === 0 && doc.type === 'inputs' ? 'no inputs' : section === 0 && doc.type === 'outputs' ? 'no declared artifacts' : '(none)'}\n`);
  }
  if (downstream && producers.length) {
    stdout.write('  affected relationships\n');
    const seen = new Set();
    for (const d of doc.dependencies) {
      if (d.orderOnly) continue;
      const r = resources.find((r) => r.id === d.resource);
      const key = `${d.resource}:${d.producer}`;
      if (seen.has(key) || (!r.root && !r.roles.includes('artifact') && !r.producer)) continue;
      seen.add(key);
      stdout.write(`    ${text(r.display)} → ${text(producer(d.producer).target, 'value.target')}${d.potential ? ' · potential glob member' : ''}\n`);
    }
  }
  if (doc.type === 'plan') {
    stdout.write('  producers\n');
    for (const p of producers) {
      if (p.kind === 'definition' && p.text) {
        stdout.write(`    ${text(p.text, 'value.literal')}\n`);
        continue;
      }
      stdout.write(`    ${text(p.target, 'value.target')} · ${p.kind}\n`);
      for (const [key, value] of Object.entries(p)) if (!['id', 'resource', 'target', 'kind', 'outputs'].includes(key)) write(value, key, 3);
    }
    stdout.write(`  stages · dependency ordering, not global barriers${doc.truncated || doc.deferred.length ? ' · partial' : ''}\n`);
    for (const stage of doc.stages) {
      stdout.write(`    stage ${stage.number}${stage.producers.length > 1 ? ' · parallel eligible' : ''}\n`);
      for (const id of stage.producers) stdout.write(`      ${text(producer(id).target, 'value.target')}\n`);
    }
    for (const d of doc.dependencies.filter((d) => d.group > 1)) stdout.write(`    sequence barrier: ${text(producer(d.producer).target, 'value.target')} · group ${d.group} follows earlier groups\n`);
  }
  if (doc.truncated) stdout.write(`  truncated: deeper ${downstream ? 'consumers' : 'producer inputs'} omitted\n`);
  if (doc.deferred.length) {
    stdout.write('  discovery boundaries\n');
    const boundaries = new Map();
    for (const d of doc.deferred) {
      const path = resources.find((r) => r.id === (d.resource || producer(d.producer)?.resource))?.display || 'consumer discovery';
      if (!boundaries.has(path)) boundaries.set(path, new Set());
      boundaries.get(path).add(d.reason);
    }
    for (const [path, attributes] of boundaries) stdout.write(`    ${text(path)} · ${[...attributes].join(' · ')}\n`);
  }
}
function writeDataResult(type, fields, bytes, includeData = true) {
  const record = { schema: 1, type, ...fields };
  if (includeData) {
    const data = Buffer.from(bytes ?? '');
    const text = data.toString('utf8');
    const valid = Buffer.from(text, 'utf8').equals(data);
    record.data = valid ? text : data.toString('base64'); record.encoding = valid ? 'utf-8' : 'base64';
  }
  stdout.write(`${JSON.stringify(record)}\n`);
}
function liveDashboard() {
  return !jsonMode && outputMode === 'ansi' && stderr.isTTY === true && env.TERM !== 'dumb' && stderr.columns >= 40 && stderr.rows >= 4 && !dashboardUnsafe && !dashboardPending;
}
function clearDashboard() {
  if (dashboardRows && (stderr.columns !== dashboardWidth || stderr.rows !== dashboardHeight)) {
    // Reflow makes the old region's coordinates unsafe to erase.
    dashboardRows = 0; dashboardUnsafe = true; stderr.write('\n'); return;
  }
  if (dashboardRows) stderr.write('\x1b[1A\r\x1b[2K'.repeat(dashboardRows));
  dashboardRows = 0;
}
function rawPublication(destination, bytes) {
  if (destination.isTTY) {
    clearDashboard();
    const data = Buffer.from(bytes);
    if (data.includes(27) || data.includes(13)) dashboardUnsafe = true;
    if (data.length) dashboardPending = data.at(-1) !== 10;
  }
  destination.write(bytes);
}
function shortField(text, width) {
  if (/[\x00-\x1f\x7f]/u.test(text)) return '(see log)';
  const segments = [...new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(text)].map(({ segment }) => ({
    segment, size: /[\ufe0f\u{1f1e6}-\u{1f1ff}]/u.test(segment) ? 2 : Math.max(...[...segment].map((rune) => displayWidth(rune.codePointAt(0)))),
  }));
  if (segments.reduce((total, item) => total + item.size, 0) <= width) return text;
  let result = '', cells = 0;
  for (const { segment, size } of segments) {
    if (cells + size > width - 1) break;
    result += segment; cells += size;
  }
  return `${result}…`;
}
function redrawDashboard() {
  if (dashboardRows && (stderr.columns !== dashboardWidth || stderr.rows !== dashboardHeight)) { clearDashboard(); return; }
  if (!liveDashboard()) { clearDashboard(); return; }
  if ((!buildProgress && !cliLockWaits.size) || stdout.writableNeedDrain || stderr.writableNeedDrain) return;
  if (watchIdle && dashboardRows && !cliLockWaits.size) return;
  const now = performance.now();
  if (now - dashboardLast < 100) return;
  dashboardLast = now;
  const erase = '\x1b[1A\r\x1b[2K'.repeat(dashboardRows);
  const width = stderr.columns, height = stderr.rows;
  dashboardWidth = width; dashboardHeight = height;
  if (cliLockWaits.size) {
    const event = cliLockWaits.values().next().value;
    const lines = [
      `  ${styled('message.warning', '! BLOCKED')}  ${shortField(`${event.kind} lock · ${lockDuration(event.elapsedMs)}`, width - 15)}`,
      lockField('lock:', event.path, 'value.path', width),
      lockField('owner:', lockOwnerText(event.owner), 'value.tool', width),
    ];
    stderr.write(`${erase}${lines.join('\n')}\n`);
    dashboardRows = lines.length;
    return;
  }
  const heading = `${presentationCommand}${presentationSubject ? ` ${shortField(presentationSubject, Math.floor(width / 3))}` : ''}${watchCycle ? ` · cycle ${watchCycle}` : ''}`;
  const lines = [styled('heading', shortField(watchIdle ? `${heading} · waiting · ${watchCycleStatus} · ${watchCycleElapsed}ms` : `${heading} · active · ${Math.max(0, Math.floor(performance.now() - buildStartedAt))}ms`, width - 2))];
  let running = 0, ready = 0, hidden = 0;
  for (let i = 0; i < cliWorkers.length; i++) {
    const worker = cliWorkers[i];
    if (!worker) continue;
    if (worker.ready) { ready++; continue; }
    running++;
    if (lines.length >= height - 3) { hidden++; continue; }
    lines.push(shortField(`  ${i}  ${shortField(worker.target, Math.floor(width / 2) - 8)}  ${worker.state || 'running'}${width >= 80 ? `  ${shortField(worker.program, Math.floor(width / 4) - 8)}  ${Math.max(0, Math.floor(now - worker.started))}ms` : ''}`, width - 2));
  }
  lines.push(shortField(`${running} running · ${Math.max(0, buildProgress.active - running - ready)} waiting · ${buildProgress.completed} done · ${buildProgress.failed} failed`, width - 2));
  if (hidden || ready) lines.push(shortField(`${hidden} hidden active · ${ready} ready services`, width - 2));
  stderr.write(`${erase}${lines.join('\n')}\n`); dashboardRows = lines.length;
}
function observeCLIEvent(event) {
  if (!buildProgress) buildProgress = { active: 0, completed: 0, failed: 0, cancelled: 0 };
  const identity = `${event.resource?.kind}:${event.resource?.name ?? event.target}:${event.node}:${event.generation}`;
  if (event.type === 'target-started' && !startedOutcomes.has(identity) && !terminalOutcomes.has(identity)) { startedOutcomes.add(identity); buildProgress.active++; }
  if (['target-completed', 'target-failed', 'target-cancelled'].includes(event.type)) {
    if (!terminalOutcomes.has(identity)) {
      terminalOutcomes.add(identity);
      if (startedOutcomes.has(identity) && buildProgress.active) buildProgress.active--;
      buildProgress[event.type === 'target-completed' ? 'completed' : event.type === 'target-failed' ? 'failed' : 'cancelled']++;
    }
  }
  if (event.type === 'process-started' && event.program) {
    if (cliWorkers.some((worker) => worker && worker.node === event.node && worker.target === event.target && worker.request === event.request && worker.generation === event.generation && worker.attempt === event.attempt && worker.program === event.program)) return;
    let slot = cliWorkers.findIndex((worker) => !worker);
    if (slot < 0) slot = cliWorkers.length;
    cliWorkers[slot] = { node: event.node, target: event.target, generation: event.generation, attempt: event.attempt, request: event.request, program: event.program, started: performance.now(), ready: false };
  }
  for (let i = 0; i < cliWorkers.length; i++) {
    const worker = cliWorkers[i];
    if (!worker || worker.node !== event.node || worker.target !== event.target || worker.generation !== event.generation || event.attempt < worker.attempt) continue;
    if (event.type === 'service-state') { worker.ready = ['ready', 'checking-health'].includes(event.state); worker.state = ['stopping', 'restarting'].includes(event.state) ? event.state === 'stopping' ? 'stopping' : 'retrying' : ''; }
    if ((event.type === 'process-exited' && (event.request === undefined || worker.request === event.request)) || ['target-completed', 'target-failed', 'target-cancelled'].includes(event.type)) cliWorkers[i] = null;
  }
}

function lockOwnerText(owner) {
  if (!owner) return 'unavailable';
  return `PID ${owner.pid}${owner.command ? ` — ${owner.command}` : ''} (recorded)`;
}

function lockDuration(milliseconds) { return `${(Math.max(0, milliseconds) / 1000).toFixed(1)}s`; }

function lockField(label, value, token, width = 0) {
  const clean = value.replace(/[\x00-\x1f\x7f]/gu, '?');
  return `  ${styled(label === 'Ctrl-C' ? 'message.tip' : 'text.muted', label.padEnd(8))}${styled(token, width ? shortField(clean, width - 12) : clean)}`;
}

function reportCacheLock(lock, state, owner, elapsedMs, context, notice) {
  const event = { schema: 1, type: `lock-${state}`, kind: 'cache', path: lock, elapsedMs, owner: owner ? {
    pid: owner.pid, command: typeof owner.command === 'string' ? owner.command.slice(0, 128).replace(/[\x00-\x1f\x7f]/gu, '?') : '', verified: false,
    ...(Number.isFinite(owner.acquiredAt) && owner.acquiredAt <= Date.now() ? { heldMs: Math.max(0, Date.now() - owner.acquiredAt) } : {}),
  } : null };
  if (context.api === true) { if (notice) context.events?.push(event); return; }
  if (state === 'waiting') cliLockWaits.set(lock, event); else cliLockWaits.delete(lock);
  if (notice) {
    if (context.json) stdout.write(`${JSON.stringify(event)}\n`);
    else if (context.human) {
      clearDashboard();
      if (state === 'acquired') stderr.write(`${styled('status.success', `  ✓ lock acquired after ${lockDuration(elapsedMs)}; continuing`)}\n`);
      else {
        const time = `waiting ${lockDuration(elapsedMs)}${event.owner?.heldMs === undefined ? '' : ` · held ${lockDuration(event.owner.heldMs)}`}`;
        stderr.write(`\n  ${styled('message.warning', '! BLOCKED')}  waiting for exclusive cache lock\n${[
          lockField('lock:', lock, 'value.path'),
          lockField('owner:', lockOwnerText(event.owner), 'value.tool'),
          lockField('time:', time, 'text.muted'),
          lockField('Ctrl-C', 'cancel this invocation; leave the lock in place', 'text.muted'),
        ].join('\n')}\n\n`);
      }
    }
  }
  redrawDashboard();
}

// cacheEntryPath stores one opaque host cache record under the project cache
// root, keyed by the runtime's opaque byte key.
function cacheEntryPath(key) {
  return join(process.cwd(), '.kame', 'cache', 'host', Buffer.from(key).toString('hex'));
}

function cacheLockPath(key) {
  return join(process.cwd(), '.kame', 'cache', 'locks', `host-${Buffer.from(key).toString('hex')}.lock`);
}

function lockOwnerAlive(owner) {
  if (!Number.isSafeInteger(owner?.pid) || owner.pid <= 0) return false;
  try {
    process.kill(owner.pid, 0);
    const start = lockProcessStart(owner.pid);
    return !owner.start || !start || owner.start === start;
  }
  catch (error) { return error.code === 'EPERM'; }
}

function lockProcessStart(pid) {
  if (process.platform !== 'linux') return undefined;
  try {
    const stat = readFileSync(`/proc/${pid}/stat`, 'utf8');
    return stat.slice(stat.lastIndexOf(')') + 2).trim().split(/\s+/u)[19];
  } catch { return undefined; }
}

async function waitForCacheLock(signal) {
  if (signal?.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
  await new Promise((resolveWait, rejectWait) => {
    let timer;
    const finish = (error) => {
      clearTimeout(timer);
      signal?.removeEventListener('abort', cancelled);
      if (error) rejectWait(error); else resolveWait();
    };
    const cancelled = () => finish(Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' }));
    timer = setTimeout(() => finish(), 20);
    signal?.addEventListener('abort', cancelled, { once: true });
  });
}

async function acquireCacheLock(key, context) {
  const signal = context.signal;
  const lock = cacheLockPath(key);
  await mkdir(dirname(lock), { recursive: true });
  const ownerFile = join(lock, 'owner.json');
  const token = randomBytes(16).toString('hex');
  const started = performance.now();
  let waiting = false, lastNotice = -5000;
  try {
    for (;;) {
      if (signal?.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
      try {
        mkdirSync(lock);
        try {
          const owner = { pid: process.pid, token, start: lockProcessStart(process.pid), acquiredAt: Date.now(), command: context.api === true ? 'node (Kame API)' : `kame ${presentationCommand}${presentationSubject ? ` ${presentationSubject}` : ''}` };
          writeFileSync(ownerFile, JSON.stringify(owner), { flag: 'wx', mode: 0o600 });
          if (waiting) reportCacheLock(lock, 'acquired', owner, Math.floor(performance.now() - started), context, true);
        } catch (error) {
          rmSync(lock, { recursive: true, force: true });
          throw error;
        }
        let released = false;
        const release = async () => {
          if (released) return;
          released = true;
          try {
            const owner = JSON.parse(await readFile(ownerFile, 'utf8'));
            if (owner.pid === process.pid && owner.token === token) await rm(lock, { recursive: true, force: true });
          } catch (error) {
            if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error;
          } finally {
            activeCacheLocks.delete(release);
          }
        };
        activeCacheLocks.add(release);
        return release;
      } catch (error) {
        if (error.code !== 'EEXIST') throw error;
        let stale = false;
        let owner;
        try {
          owner = JSON.parse(await readFile(ownerFile, 'utf8'));
          stale = !lockOwnerAlive(owner);
        } catch (ownerError) {
          if (ownerError.code === 'ENOENT' || ownerError instanceof SyntaxError) {
            try { stale = Date.now() - statSync(lock).mtimeMs > 2000; } catch (statError) { if (statError.code === 'ENOENT') stale = true; else throw statError; }
          } else if (ownerError.code === 'ENOENT') stale = true;
          else throw ownerError;
        }
        if (stale) {
          await rm(lock, { recursive: true, force: true });
          continue;
        }
        waiting = true;
        const elapsedMs = Math.floor(performance.now() - started);
        const notice = elapsedMs - lastNotice >= 5000;
        if (notice) lastNotice = elapsedMs;
        reportCacheLock(lock, 'waiting', lockOwnerAlive(owner) ? owner : null, elapsedMs, context, notice);
        await waitForCacheLock(signal);
      }
    }
  } finally { if (context.api !== true) cliLockWaits.delete(lock); }
}

const cacheRecordsPerBackend = 1024;
function pruneCacheDirectory(directory) {
  let names;
  try { names = readdirSync(directory).sort(); }
  catch (error) { if (error.code === 'ENOENT') return; throw error; }
  const records = [];
  for (const name of names) {
    if (name.startsWith('.kame-write-')) continue;
    try {
      const info = lstatSync(join(directory, name), { bigint: true });
      if (!info.isFile()) continue;
      records.push({ name, time: info.mtimeNs });
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
  records.sort((a, b) => a.time < b.time ? -1 : a.time > b.time ? 1 : a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
  const remove = records.length - cacheRecordsPerBackend;
  for (let i = 0; i < remove; i++) {
    try { unlinkSync(join(directory, records[i].name)); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
}

function displayWidth(code) {  if ((code >= 0x0300 && code <= 0x036f) || (code >= 0x1ab0 && code <= 0x1aff) || (code >= 0x1dc0 && code <= 0x1dff) || (code >= 0x20d0 && code <= 0x20ff) || (code >= 0xfe00 && code <= 0xfe0f) || (code >= 0xfe20 && code <= 0xfe2f)) return 0;
  if (code >= 0x1100 && (code <= 0x115f || code === 0x2329 || code === 0x232a || (code >= 0x2e80 && code <= 0xa4cf) || (code >= 0xac00 && code <= 0xd7a3) || (code >= 0xf900 && code <= 0xfaff) || (code >= 0xfe10 && code <= 0xfe19) || (code >= 0xfe30 && code <= 0xfe6f) || (code >= 0xff00 && code <= 0xff60) || (code >= 0xffe0 && code <= 0xffe6) || (code >= 0x20000 && code <= 0x3fffd))) return 2;
  return 1;
}

function runeAt(text, at) {
  const code = text.codePointAt(at);
  return { code, size: code > 0xffff ? 2 : 1 };
}

// Portable source spans count UTF-8 bytes, not JavaScript UTF-16 code units.
function sourceIndex(text, byteOffset) {
  const bytes = Buffer.from(text, 'utf8');
  const offset = Math.max(0, Math.min(byteOffset, bytes.length));
  return bytes.subarray(0, offset).toString('utf8').length;
}

function sourcePosition(text, offset) {
  if (offset < 0) offset = 0;
  if (offset > text.length) offset = text.length;
  let line = 1;
  let column = 1;
  let i = 0;
  while (i < offset) {
    const b = text[i];
    if (b === '\n') { line++; column = 1; i++; continue; }
    if (b === '\r' && i + 1 < text.length && text[i + 1] === '\n') { i++; continue; }
    if (b === '\t') { column += 8 - (column - 1) % 8; i++; continue; }
    const rune = runeAt(text, i);
    if (i + rune.size > offset) break;
    column += displayWidth(rune.code);
    i += rune.size;
  }
  return { line, column };
}

function lineBounds(text, offset) {
  if (offset < 0) offset = 0;
  if (offset > text.length) offset = text.length;
  let start = offset;
  while (start > 0 && text[start - 1] !== '\n') start--;
  let end = offset;
  while (end < text.length && text[end] !== '\n' && text[end] !== '\r') end++;
  return [start, end];
}

function excerptSegmentEnd(text, start, limit, width) {
  let column = 1;
  let i = start;
  while (i < limit) {
    let cells = 1;
    let size = 1;
    if (text[i] === '\t') {
      cells = 8 - (column - 1) % 8;
    } else {
      const rune = runeAt(text, i);
      cells = displayWidth(rune.code);
      size = rune.size;
    }
    if (i !== start && column - 1 + cells > width) break;
    column += cells;
    i += size;
  }
  return i;
}

function marker(text, lineStart, start, end, lineEnd) {
  let out = '';
  let column = 1;
  for (let i = lineStart; i < start;) {
    if (text[i] === '\t') { out += '\t'; column += 8 - (column - 1) % 8; i++; continue; }
    const rune = runeAt(text, i);
    out += ' '.repeat(displayWidth(rune.code));
    column += displayWidth(rune.code);
    i += rune.size;
  }
  if (end < start) end = start;
  if (end > lineEnd) end = lineEnd;
  let width = 0;
  for (let i = start; i < end;) {
    if (text[i] === '\t') { const cells = 8 - (column - 1) % 8; width += cells; column += cells; i++; continue; }
    const rune = runeAt(text, i);
    width += displayWidth(rune.code);
    column += displayWidth(rune.code);
    i += rune.size;
  }
  if (width < 1) width = 1;
  return `${out}^${'~'.repeat(width - 1)}\n`;
}

function wrappedExcerpt(text, lineStart, lineEnd, start, end, width) {
  if (lineStart === lineEnd) return `\n${marker(text, lineStart, start, end, lineEnd)}`;
  let out = '';
  let marked = false;
  let segmentStart = lineStart;
  while (segmentStart < lineEnd) {
    const segmentEnd = excerptSegmentEnd(text, segmentStart, lineEnd, width);
    out += `${text.slice(segmentStart, segmentEnd)}\n`;
    if (!marked && start >= segmentStart && (start < segmentEnd || (segmentEnd === lineEnd && start === segmentEnd))) {
      out += marker(text, segmentStart, start, end, segmentEnd);
      marked = true;
    }
    segmentStart = segmentEnd;
  }
  return out;
}

function severityName(severity) {
  if (severity === 'warning') return 'warning';
  if (severity === 'fatal') return 'fatal';
  return 'error';
}

// renderDiagnostic reproduces the native plain/human diagnostic rendering from
// the event's structured diagnostic and the primary source text.
function renderDiagnostic(d, source, width) {
  if (width < 20) width = 80;
  let out = '';
  const renderSource = diagnosticSource(d.source, source);
  const style = diagnosticColor && diagnosticFormat === 'human';
  const label = styled(`message.${severityName(d.severity)}`, severityName(d.severity), style);
  const code = styled('diagnostic.code', d.code, style);
  if (d.target) {
    if (diagnosticFormat === 'human') out += `${label} [${d.target}] ${code}\n`;
    else out += `${d.target} failed: ${d.code}\n`;
    if (d.targetStack && d.targetStack.length > 1) {
      out += `  required by ${d.targetStack.join(' -> ')}\n`;
    }
  }
  const span = d.span ?? { start: 0, end: 0 };
  if (d.source && renderSource && d.source === renderSource.name) {
    const sourceStart = sourceIndex(renderSource.text, span.start);
    const sourceEnd = sourceIndex(renderSource.text, span.end);
    const pos = sourcePosition(renderSource.text, sourceStart);
    out += `${d.source}:${pos.line}:${pos.column}: ${label} ${code}: ${d.message}\n`;
    let start = sourceStart;
    if (start < 0) start = 0;
    if (start > renderSource.text.length) start = renderSource.text.length;
    const [lineStart, lineEnd] = lineBounds(renderSource.text, start);
    out += wrappedExcerpt(renderSource.text, lineStart, lineEnd, start, sourceEnd, width);
  } else if (d.source) {
    out += `${d.source}: ${label} ${code}: ${d.message}\n`;
  } else {
    out += `${label} ${code}: ${d.message}\n`;
  }
  for (const note of d.notes ?? []) out += `note: ${note}\n`;
  for (const related of d.related ?? []) {
    out += 'note: ';
    if (related.source) {
      const relatedSource = diagnosticSource(related.source, renderSource);
      if (relatedSource && relatedSource.name === related.source) {
        const pos = sourcePosition(relatedSource.text, sourceIndex(relatedSource.text, (related.span ?? { start: 0 }).start));
        out += `${related.source}:${pos.line}:${pos.column}: `;
      } else {
        out += `${related.source}: `;
      }
    }
    out += `${related.message}\n`;
  }
  for (const frame of d.frames ?? []) {
    out += 'while ';
    if (frame.kind) out += `${frame.kind} `;
    out += frame.label;
    if (frame.source) {
      const frameSource = diagnosticSource(frame.source, renderSource);
      if (frameSource && frameSource.name === frame.source) {
        const pos = sourcePosition(frameSource.text, sourceIndex(frameSource.text, (frame.span ?? { start: 0 }).start));
        out += ` at ${frame.source}:${pos.line}:${pos.column}`;
      } else {
        out += ` at ${frame.source}`;
      }
    }
    out += '\n';
  }
  for (const tip of d.tips ?? []) out += `help: ${tip}\n`;
  if (d.cause && d.cause.kind) {
    out += `caused by: ${d.cause.message}`;
    if (d.cause.status !== undefined) out += ` (status ${d.cause.status})`;
    if (d.cause.signal !== undefined) out += ` (signal ${d.cause.signal})`;
    out += '\n';
  }
  return out;
}

// diagnosticSource uses the primary source when the names match, otherwise
// reads the referenced file for rendering only.
function diagnosticSource(name, primary) {
  if (sourceTexts.has(name)) return { name, text: sourceTexts.get(name) };
  if (!name || (primary && primary.name === name)) return primary;
  try {
    return { name, text: readFileSync(name, 'utf8') };
  } catch {
    return null;
  }
}

function humanEvent(bytes) {
  const event = JSON.parse(new TextDecoder().decode(bytes));
  if (event.type === 'target-completed' && event.resource?.kind === 'definition') return;
  if (event.type === 'stdout') { rawPublication(stdout, eventData(event)); return; }
  if (event.type === 'stderr') { rawPublication(stderr, eventData(event)); return; }
  if (outputMode === 'ansi' && ['target-reason', 'process-started', 'process-exited', 'target-started', 'service-state'].includes(event.type)) return;
  if (!['target-reason', 'process-started', 'process-exited', 'target-started', 'target-completed', 'target-failed', 'target-cancelled', 'service-state', 'cache-warning'].includes(event.type)) return;
  clearDashboard();
  if (event.type === 'target-reason') {
    stderr.write(`${styled('message.info', 'info')} [${event.target}] ${event.decision}: ${event.message}${event.dependency?.resource?.name ? `: ${event.dependency.resource.name}` : ''}\n`);
    return;
  }
  if (event.type === 'process-started') {
    if (liveDashboard()) return;
    if (event.program) {
      const argv = Array.isArray(event.argv) ? event.argv : [];
      stderr.write(`${styled('status.running', 'process')} [${event.target}] process ${event.program}${argv.length ? ` ${argv.join(' ')}` : ''}${event.displayTruncated ? ' …' : ''}\n`);
    }
    return;
  }
  if (event.type === 'process-exited') {
    if (event.runtimeMS !== undefined) stderr.write(`process [${event.target}] finished in ${event.runtimeMS}ms\n`);
    return;
  }
  if (event.type === 'target-started') {
    if (liveDashboard()) return;
    stderr.write(`started [${event.target}]\n`);
    return;
  }
  if (event.type === 'target-completed') {
    stderr.write(`${styled('status.success', `done [${event.target}] complete`)}\n`);
    return;
  }
  if (event.type === 'target-failed') {
    stderr.write(`${styled('status.failed', `error [${event.target}] failed${outcomeCause(event.diagnostic)}`)}\n`);
    if (event.diagnostic) lastDiagnostic = event.diagnostic;
    return;
  }
  if (event.type === 'target-cancelled') {
    stderr.write(`${styled('status.cancelled', `cancelled [${event.target}] cancelled${outcomeCause(event.diagnostic)}`)}\n`);
    if (event.diagnostic) lastDiagnostic = event.diagnostic;
    return;
  }
  if (event.type === 'service-state') {
    stderr.write(`info [${event.target}] service ${event.state} (generation ${event.generation}, attempt ${event.attempt})\n`);
    return;
  }
  if (event.type === 'cache-warning' && event.diagnostic) {
    stderr.write(renderDiagnostic(event.diagnostic, primarySource, 80));
  }
}

function outcomeCause(diagnostic) {
  if (!diagnostic?.code) return '';
  const cause = diagnostic.cause;
  return ` ${diagnostic.code}: ${diagnostic.message}${cause?.status !== undefined ? ` (status ${cause.status})` : ''}${cause?.signal !== undefined ? ` (signal ${cause.signal})` : ''}`;
}

function eventData(event) {
  if (event.data === undefined) return '';
  if (event.encoding === 'base64') return Buffer.from(event.data, 'base64');
  return event.data;
}

function printSummary(counts = buildProgress, status = 0, elapsedMS = performance.now() - buildStartedAt, command = presentationCommand) {
  clearDashboard();
  const label = status >= 128 || (counts.cancelled && !counts.failed && status) ? 'cancelled' : status && (presentationCommand !== 'fmt' || invocationHadDiagnostic) ? 'error' : 'done';
  stderr.write(`${styled(label === 'done' ? 'status.success' : label === 'error' ? 'status.failed' : 'status.cancelled', label)} ${command} · ${counts.completed} ${presentationCommand === 'fmt' ? 'files' : 'targets'} complete · ${counts.failed} failed · ${counts.cancelled} cancelled · ${Math.max(0, Math.floor(elapsedMS))}ms\n`);
}

function diagnosticError(text, fallbackCode) {
  if (typeof text === 'string' && text.length !== 0) {
    const at = text.indexOf(': ');
    if (at > 0) return Object.assign(new Error(text.slice(at + 2)), { code: text.slice(0, at) });
    return Object.assign(new Error(text), { code: fallbackCode });
  }
  return Object.assign(new Error(fallbackCode), { code: fallbackCode });
}

function encodeJSON(value) {
  if (Array.isArray(value)) return `[${value.map(encodeJSON).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${encodeJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

function isPathTarget(target) {
  return target.startsWith('/') || target.startsWith('./') || target.includes('/');
}

// Glob matching mirrors program.wildcard: a recursive walk from the pattern's
// first wildcard root, segment matching with **, and "./"-prefixed relative
// results.
function matchSegment(pattern, name) {
  let pi = 0;
  let ni = 0;
  let star = -1;
  let mark = 0;
  while (ni < name.length) {
    if (pattern[pi] === '\\' && pi + 1 < pattern.length) {
      if (pattern[pi + 1] === name[ni]) { pi += 2; ni++; continue; }
      if (star >= 0) { pi = star + 1; ni = ++mark; continue; }
      return false;
    }
    if (pi < pattern.length && pattern[pi] === '*') {
      star = pi++;
      mark = ni;
      continue;
    }
    if (pi < pattern.length && (pattern[pi] === '?' || (pattern[pi] !== '[' && pattern[pi] === name[ni]))) {
      pi++;
      ni++;
      continue;
    }
    if (pi < pattern.length && pattern[pi] === '[') {
      const result = matchClass(pattern, pi, name[ni]);
      if (result.ok) {
        pi = result.next;
        ni++;
        continue;
      }
      if (star >= 0) {
        pi = star + 1;
        ni = ++mark;
        continue;
      }
      return false;
    }
    if (star >= 0) {
      pi = star + 1;
      ni = ++mark;
      continue;
    }
    return false;
  }
  while (pi < pattern.length && pattern[pi] === '*') pi++;
  return pi === pattern.length;
}

function matchClass(pattern, pi, ch) {
  let i = pi + 1;
  let negate = false;
  if (pattern[i] === '^' || pattern[i] === '!') {
    negate = true;
    i++;
  }
  let matched = false;
  let first = true;
  while (i < pattern.length && (pattern[i] !== ']' || first)) {
    first = false;
    let start = pattern[i++];
    if (start === '\\') {
      if (i >= pattern.length) return { ok: false, next: pi + 1 };
      start = pattern[i++];
    }
    if (pattern[i] === '-' && i + 1 < pattern.length && pattern[i + 1] !== ']') {
      i++;
      let end = pattern[i++];
      if (end === '\\') {
        if (i >= pattern.length) return { ok: false, next: pi + 1 };
        end = pattern[i++];
      }
      if (ch >= start && ch <= end) matched = true;
    } else {
      if (start === ch) matched = true;
    }
  }
  if (i >= pattern.length || pattern[i] !== ']') return { ok: false, next: pi + 1 };
  return { ok: matched !== negate, next: i + 1 };
}

function nextSegment(value, end) {
  return end < value.length ? end + 1 : end;
}

function matchSegments(pattern, pi, name, ni) {
  if (pi === pattern.length) return ni === name.length;
  let pend = pi;
  while (pend < pattern.length && pattern[pend] !== '/') pend++;
  let nend = ni;
  while (nend < name.length && name[nend] !== '/') nend++;
  const segment = pattern.slice(pi, pend);
  if (segment === '**') {
    if (pend === pattern.length) return true;
    if (matchSegments(pattern, nextSegment(pattern, pend), name, ni)) return true;
    for (let cursor = ni; cursor < name.length; cursor++) {
      if (name[cursor] === '/' && matchSegments(pattern, nextSegment(pattern, pend), name, cursor + 1)) return true;
    }
    return false;
  }
  if (ni === name.length) return false;
  if (!matchSegment(segment, name.slice(ni, nend))) return false;
  if (pend === pattern.length || nend === name.length) return pend === pattern.length && nend === name.length;
  return matchSegments(pattern, pend + 1, name, nend + 1);
}

function globRoot(pattern) {
  let cut = -1;
  for (let i = 0; i < pattern.length; i++) {
    if (pattern[i] === '*' || pattern[i] === '?' || pattern[i] === '[' || pattern[i] === '\\') { cut = i; break; }
  }
  if (cut < 0) return pattern;
  while (cut > 0 && pattern[cut - 1] !== '/') cut--;
  if (cut === 0) return '.';
  if (cut === 1) return '/';
  return pattern.slice(0, cut - 1);
}

function collectPaths(directory, names) {
  let entries;
  try {
    entries = readdirSync(directory, { withFileTypes: true });
  } catch (error) {
    if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') throw error;
    return;
  }
  for (const entry of entries) {
    const name = join(directory, entry.name);
    names.push(name);
    if (entry.isDirectory()) collectPaths(name, names);
  }
}

function isMemoryResource(name) { return name.startsWith('mem://'); }
function isFileResource(name) { return name.startsWith('file://'); }
function filesystemPath(name) { return isFileResource(name) ? fileURLToPath(name) : name; }

function memoryResourceExists(name) {
  if (memoryResourceFiles.has(name) || memoryResourceDirectories.has(name)) return true;
  let prefix = name;
  if (!prefix.endsWith('/')) prefix += '/';
  for (const path of memoryResourceFiles.keys()) if (path.startsWith(prefix)) return true;
  for (const path of memoryResourceDirectories) if (path.startsWith(prefix)) return true;
  return false;
}

function memoryResourceDirectory(name) {
  if (memoryResourceDirectories.has(name)) return true;
  let prefix = name;
  if (!prefix.endsWith('/')) prefix += '/';
  for (const path of memoryResourceFiles.keys()) if (path.startsWith(prefix)) return true;
  for (const path of memoryResourceDirectories) if (path.startsWith(prefix)) return true;
  return false;
}

function readResource(name) {
  if (isMemoryResource(name)) {
    const data = memoryResourceFiles.get(name);
    if (!data) throw Object.assign(new Error('memory resource does not exist'), { code: 'ENOENT' });
    return Promise.resolve(Buffer.from(data));
  }
  return readFile(filesystemPath(name));
}

function statResource(name, options, noFollow = false) {
	if (!isMemoryResource(name)) return (noFollow ? lstat : stat)(filesystemPath(name), options);
  if (memoryResourceFiles.has(name)) {
    const data = memoryResourceFiles.get(name);
    const stamp = memoryResourceClock;
    return Promise.resolve({
      size: data.byteLength,
      mode: 0o100644,
      mtimeNs: stamp,
      isFile: () => true,
      isDirectory: () => false,
      isSymbolicLink: () => false,
    });
  }
  if (memoryResourceDirectory(name)) {
    return Promise.resolve({ size: 0, mode: 0o040755, mtimeNs: memoryResourceClock, isFile: () => false, isDirectory: () => true, isSymbolicLink: () => false });
  }
  throw Object.assign(new Error('memory resource does not exist'), { code: 'ENOENT' });
}

function resourceExists(name) {
  if (isMemoryResource(name)) return memoryResourceExists(name);
  return existsSync(filesystemPath(name));
}

function resourceWildcard(pattern) {
  if (isMemoryResource(pattern)) {
    return [...new Set([...memoryResourceFiles.keys(), ...memoryResourceDirectories])].filter((name) => matchSegments(pattern, 0, name, 0)).sort();
  }
  if (isFileResource(pattern)) {
    return wildcardPaths(filesystemPath(pattern)).map((name) => pathToFileURL(name).href);
  }
  return wildcardPaths(pattern);
}

function writeResource(name, data) {
  if (isMemoryResource(name)) {
    memoryResourceClock++;
    memoryResourceFiles.set(name, Buffer.from(data));
    let parent = name.slice(0, name.lastIndexOf('/'));
    while (parent.startsWith('mem://') && parent.length >= 'mem://x'.length) {
      memoryResourceDirectories.add(parent);
      const slash = parent.lastIndexOf('/');
      if (slash <= parent.indexOf('://') + 2) break;
      parent = parent.slice(0, slash);
    }
    return Promise.resolve();
  }
  return writeFileAtomic(filesystemPath(name), data);
}

function wildcardPaths(pattern) {
  while (pattern.startsWith('./')) pattern = pattern.slice(2);
  const names = [];
  const root = globRoot(pattern);
  if (root === pattern) {
    try { lstatSync(root); names.push(root); }
    catch (error) { if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') throw error; }
  } else collectPaths(root, names);
  const matches = [];
  for (const name of names) {
    if (matchSegments(pattern, 0, name, 0)) matches.push(name.startsWith('/') ? name : `./${name}`);
  }
  matches.sort();
  return matches;
}

async function writeFileAtomic(name, data, durable = false) {
  name = filesystemPath(name);
  const directory = dirname(name);
  await mkdir(directory, { recursive: true });
  const staging = await mkdtemp(join(directory, '.kame-write-'));
  try {
    const temporary = join(staging, 'output');
    const file = await open(temporary, 'wx', 0o644);
    try {
      await file.writeFile(data);
      if (durable) await file.sync();
    } finally { await file.close(); }
    await rename(temporary, name);
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

function isExecutable(file) {
  try {
    const info = statSync(file);
    return info.isFile() && (info.mode & 0o111) !== 0;
  } catch {
    return false;
  }
}

function resolveTool(name, overrides = [], cache) {
  if (cache?.has(name)) return cache.get(name);
  const identity = name;
  const prefix = `${name}=`;
  for (let i = overrides.length - 1; i >= 0; i--) {
    if (overrides[i].startsWith(prefix)) { name = overrides[i].slice(prefix.length); break; }
  }
  let selected = '';
  if (name.includes('/')) {
    const absolute = resolve(process.cwd(), name);
    if (isExecutable(absolute)) selected = absolute;
  } else if (name) {
    for (const directory of (env.PATH ?? '').split(':')) {
      const candidate = resolve(process.cwd(), directory, name);
      if (isExecutable(candidate)) { selected = candidate; break; }
    }
  }
  cache?.set(identity, selected);
  return selected;
}

function readStdin() {
  return new Promise((resolveRead) => {
    const chunks = [];
    process.stdin.on('data', (chunk) => chunks.push(chunk));
    process.stdin.on('end', () => resolveRead(Buffer.concat(chunks).toString('utf8')));
  });
}

const REQUIRED_EXPORTS = [
  'memory',
  'kame_wasm_abi_version',
  'kame_wasm_alloc',
  'kame_wasm_copy',
  'kame_wasm_instance_create',
  'kame_wasm_instance_free',
  'kame_wasm_source_compile',
  'kame_wasm_register_plugins',
  'kame_wasm_set_source_name',
  'kame_wasm_parse',
  'kame_wasm_format',
  'kame_wasm_cli',
  'kame_wasm_target_operands',
  'kame_wasm_expression_begin',
  'kame_wasm_expression_cancel',
  'kame_wasm_expression_effect_kind',
  'kame_wasm_expression_effect_length',
  'kame_wasm_expression_effect_copy',
  'kame_wasm_set_forwarding',
  'kame_wasm_set_directory',
  'kame_wasm_target_begin',
  'kame_wasm_prepare',
  'kame_wasm_plan',
  'kame_wasm_graph',
  'kame_wasm_tools',
  'kame_wasm_set_tool_path',
  'kame_wasm_tools_check',
  'kame_wasm_inspection_grant',
  'kame_wasm_step',
  'kame_wasm_next_event_header',
  'kame_wasm_next_request_kind',
  'kame_wasm_next_request_id',
  'kame_wasm_next_request_data_length',
  'kame_wasm_request_data_copy',
  'kame_wasm_next_request_key_length',
  'kame_wasm_request_key_copy',
  'kame_wasm_next_request_record_length',
  'kame_wasm_request_record_copy',
  'kame_wasm_event_payload_copy',
  'kame_wasm_complete_bytes',
  'kame_wasm_complete_text',
  'kame_wasm_complete_nil',
  'kame_wasm_complete_json',
  'kame_wasm_complete_failure',
  'kame_wasm_result_copy',
  'kame_wasm_result_kind',
  'kame_wasm_target_event',
  'kame_wasm_process_started',
  'kame_wasm_process_exited_request',
  'kame_wasm_process_retain_limit',
  'kame_wasm_process_stream',
  'kame_wasm_process_terminal',
  'kame_wasm_declaration_predicate',
  'kame_wasm_composition_condition',
  'kame_wasm_instance_diagnostic_length',
  'kame_wasm_instance_diagnostic_copy',
  'kame_wasm_instance_diagnostic_span',
  'kame_wasm_watch_begin',
  'kame_wasm_watch_invalidate',
  'kame_wasm_watch_state',
  'kame_wasm_watch_cancel',
];

class Module {
  constructor(exports) {
    this.exports = exports;
    this.path = wasmPath;
    this.scratchBuffers = new Map();
    this.cacheLeases = new Map();
    this.processGroups = new Map();
    this.cacheWrites = new Map();
  }

  static async load(path = wasmPath) {
    let bytes;
    try {
      bytes = await readFile(path);
    } catch (error) {
      throw Object.assign(new Error(`cannot read ${path}: ${error.message}`), { code: 'FS_ERR' });
    }
    const { instance } = await WebAssembly.instantiate(bytes, {});
    const exports = instance.exports;
    const missing = REQUIRED_EXPORTS.filter((name) => !(name in exports));
    if (missing.length !== 0) throw Object.assign(new Error(`WASM ABI is incomplete: missing ${missing.join(', ')}`), { code: 'FEATURE_UNSUP' });
    if (exports.kame_wasm_abi_version() !== 1) throw Object.assign(new Error('unsupported WASM ABI schema'), { code: 'FEATURE_UNSUP' });
    const module = new Module(exports);
    module.path = path;
    return module;
  }

  abiInfo() {
    return { schema: this.exports.kame_wasm_abi_version(), stage: STAGE, wasm: this.path, exports: Object.keys(this.exports).sort() };
  }

  allocate(size, alignment = 1) {
    const pointer = this.exports.kame_wasm_alloc(size, alignment);
    if (pointer === 0) throw Object.assign(new Error(`WASM allocation failed for ${size} bytes`), { code: 'NO_MEMORY' });
    return pointer;
  }

  // ABI calls copy these inputs before returning. Separate slots keep fields
  // that are passed together alive, and geometric growth bounds abandoned
  // buffers in the ABI's monotonic host allocator.
  scratch(name, size, alignment = 1) {
    let buffer = this.scratchBuffers.get(name);
    if (!buffer || buffer.capacity < size) {
      const capacity = Math.max(64, 2 ** Math.ceil(Math.log2(Math.max(1, size))));
      buffer = { pointer: this.allocate(capacity, alignment), capacity };
      this.scratchBuffers.set(name, buffer);
    }
    return buffer.pointer;
  }

  write(bytes) {
    const data = typeof bytes === 'string' ? new TextEncoder().encode(bytes) : bytes;
    const pointer = this.allocate(data.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, data.length).set(data);
    return { pointer, length: data.length };
  }

  decode(pointer, length) {
    return new TextDecoder().decode(new Uint8Array(this.exports.memory.buffer, pointer, length));
  }

  instanceDiagnostic(instance) {
    const length = this.exports.kame_wasm_instance_diagnostic_length(instance);
    if (length === 0) return null;
    const pointer = this.allocate(length || 1);
    if (this.exports.kame_wasm_instance_diagnostic_copy(instance, pointer, length) !== 0) return null;
    return this.decode(pointer, length);
  }

  instanceDiagnosticSpan(instance) {
    const startPointer = this.allocate(4, 4);
    const endPointer = this.allocate(4, 4);
    if (this.exports.kame_wasm_instance_diagnostic_span(instance, startPointer, endPointer) !== 0) return null;
    const view = new DataView(this.exports.memory.buffer);
    return { start: view.getInt32(startPointer, true), end: view.getInt32(endPointer, true) };
  }

  // compileFailure builds the error for a source_compile failure, carrying the
  // diagnostic's source span when the ABI reports one.
  compileFailure(instance, fallbackCode) {
    const error = diagnosticError(this.instanceDiagnostic(instance), fallbackCode);
    const span = this.instanceDiagnosticSpan(instance);
    if (span !== null) error.span = span;
    // Failed portable registration can expose several authored diagnostics.
    try {
      const bytes = this.copyQuery((handle, dst, capacity, length) => this.exports.kame_wasm_target_event(handle, dst, capacity, length), instance);
      if (bytes.length) error.diagnostics = this.decode0(bytes).trim().split('\n').map((line) => JSON.parse(line).diagnostic);
    } catch { /* source_compile can fail before a runtime exists. */ }
    return error;
  }

  completeJSON(instance, request, value) {
    const bytes = new TextEncoder().encode(encodeJSON(value));
    const pointer = this.allocate(bytes.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
    return this.exports.kame_wasm_complete_json(instance, request, pointer, bytes.length);
  }

  // completeInt completes with an exact 64-bit integer. The digits are built
  // from a BigInt so large nanosecond clocks do not lose precision.
  completeInt(instance, request, value) {
    const bytes = new TextEncoder().encode(String(value));
    const pointer = this.allocate(bytes.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
    return this.exports.kame_wasm_complete_json(instance, request, pointer, bytes.length);
  }

  completeFailure(instance, request, code, message) {
    const codeBytes = this.write(code);
    const messageBytes = this.write(message);
    return this.exports.kame_wasm_complete_failure(instance, request, codeBytes.pointer, codeBytes.length, messageBytes.pointer, messageBytes.length);
  }

  // copyResult runs a query-then-copy through a "did the call copy a value"
  // helper. call(pointer, length, lengthPointer) returns a kame_wasm_status.
  copyQuery(call, instance) {
    const lengthPointer = this.allocate(4, 4);
    const query = call(instance, 0, 0, lengthPointer);
    if (query !== 0 && query !== 3) throw diagnosticError(this.instanceDiagnostic(instance), 'EXPR_INVALID');
    const length = new DataView(this.exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
    const output = this.allocate(length || 1);
    if (call(instance, output, length, lengthPointer) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'EXPR_INVALID');
    return new Uint8Array(this.exports.memory.buffer, output, length).slice();
  }

  async copyInspectionQuery(call, instance, context) {
    const lengthPointer = this.allocate(4, 4);
    let output = 0, capacity = 0;
    for (;;) {
      const query = call(instance, output, capacity, lengthPointer);
      if (query === 0) {
        const length = new DataView(this.exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
        return new Uint8Array(this.exports.memory.buffer, output, length).slice();
      }
      if (query === 3) {
        capacity = new DataView(this.exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
        output = this.allocate(capacity || 1);
        continue;
      }
      if (query !== 6) throw diagnosticError(this.instanceDiagnostic(instance), `inspection query failed (${query})`);
      if (this.exports.kame_wasm_step(instance) !== 1) throw Object.assign(new Error('inspection host request unavailable'), { code: 'HOST_FAIL' });
      if (await this.service(instance, context) !== 0) throw Object.assign(new Error('inspection host completion failed'), { code: 'HOST_FAIL' });
    }
  }

  async declarationPredicate(descriptor) {
    return this.configurationCondition(descriptor, this.exports.kame_wasm_declaration_predicate);
  }

  async compositionCondition(descriptor) {
    return this.configurationCondition(descriptor, this.exports.kame_wasm_composition_condition);
  }

  async configurationCondition(descriptor, query) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const bytes = this.write(JSON.stringify(descriptor));
      const result = this.copyQuery((handle, dst, capacity, length) => query(handle, bytes.pointer, bytes.length, dst, capacity, length), instance);
      return new TextDecoder().decode(result) === 'true';
    } catch (error) {
      const text = this.instanceDiagnostic(instance);
      if (text?.trimStart().startsWith('{')) {
        const detail = JSON.parse(text).diagnostic;
        if (detail) throw Object.assign(diagnosticError(`${detail.code}: ${detail.message}`, 'EXPR_INVALID'), { span: detail.span, diagnostics: [detail] });
      }
      throw error;
    } finally { this.exports.kame_wasm_instance_free(instance); }
  }

  async parseCLI(command, args) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const commandBytes = this.write(command);
      const argsBytes = this.write(args.join('\u0000'));
      const call = (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_cli(handle, commandBytes.pointer, commandBytes.length, argsBytes.pointer, argsBytes.length, dst, dstLen, lengthPointer);
      return JSON.parse(this.decode0(this.copyQuery(call, instance)));
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  async service(instance, context) {
    const header = this.allocate(48, 8);
    if (this.exports.kame_wasm_next_event_header(instance, header, 48) !== 0) throw Object.assign(new Error('request header unavailable'), { code: 'HOST_FAIL' });
    const view = new DataView(this.exports.memory.buffer, header, 48);
    const request = view.getBigUint64(20, true);
    const payloadLength = view.getUint32(44, true);
    const kind = this.exports.kame_wasm_next_request_kind(instance);
    // Raw engine identity is distinct from the opaque ABI completion handle.
    // Read it while pinned; handle slots may be retired/reused after detach.
    const rawRequest = this.exports.kame_wasm_next_request_id(instance);
    const finalizationActive = this.exports.kame_wasm_finalization_tick?.(instance, process.hrtime.bigint(), 0) === 1;
    const cleanupMS = Number(this.exports.kame_wasm_finalization_budget?.(instance, 0) ?? 0n);
    let data = null;
    if (kind === 2) {
      const dataLength = this.exports.kame_wasm_next_request_data_length(instance);
      const dataPointer = this.allocate(dataLength || 1);
      if (this.exports.kame_wasm_request_data_copy(instance, dataPointer, dataLength) !== 0) throw Object.assign(new Error('request data copy failed'), { code: 'HOST_FAIL' });
      data = new Uint8Array(this.exports.memory.buffer, dataPointer, dataLength).slice();
    }
    let key = null;
    let record = null;
    if (kind === 10 || kind === 11 || kind === 12 || kind === 24 || kind === 25) {
      const keyLength = this.exports.kame_wasm_next_request_key_length(instance);
      const keyPointer = this.allocate(keyLength || 1);
      if (this.exports.kame_wasm_request_key_copy(instance, keyPointer, keyLength) !== 0) throw Object.assign(new Error('cache key copy failed'), { code: 'HOST_FAIL' });
      key = new Uint8Array(this.exports.memory.buffer, keyPointer, keyLength).slice();
      if (kind === 11) {
        const recordLength = this.exports.kame_wasm_next_request_record_length(instance);
        const recordPointer = this.allocate(recordLength || 1);
        if (this.exports.kame_wasm_request_record_copy(instance, recordPointer, recordLength) !== 0) throw Object.assign(new Error('cache record copy failed'), { code: 'HOST_FAIL' });
        record = new Uint8Array(this.exports.memory.buffer, recordPointer, recordLength).slice();
      }
    }
    const payloadPointer = this.allocate(payloadLength || 1);
    if (this.exports.kame_wasm_event_payload_copy(instance, payloadPointer, payloadLength) !== 0) throw Object.assign(new Error('request payload copy failed'), { code: 'HOST_FAIL' });
    const payload = this.decode(payloadPointer, payloadLength);
    if (context.concurrent && this.exports.kame_wasm_request_detach(instance, request) !== 0) throw Object.assign(new Error('cannot unpin host request'), { code: 'HOST_FAIL' });
    let cleanupTimer;
    let notifyCancellation;
    const bodySignal = context.signal;
    context = { ...context, finalizationActive };
    if (finalizationActive && cleanupMS === 0 && bodySignal) {
      notifyCancellation = () => this.exports.kame_wasm_finalization_tick(instance, process.hrtime.bigint(), 1);
      bodySignal.addEventListener('abort', notifyCancellation, { once: true });
      if (bodySignal.aborted) notifyCancellation();
    }
    if (cleanupMS !== 0 && kind !== 23) {
      const cleanup = new AbortController();
      if (cleanupMS < 0) cleanup.abort();
      else cleanupTimer = setTimeout(() => cleanup.abort(), cleanupMS);
      // Only runtime-designated cleanup is shielded. All host policy and grants
      // are inherited unchanged; this signal does not authorize other work.
      context = { ...context, signal: cleanup.signal, timeoutMS: Math.max(1, cleanupMS), finalizing: true };
    }
    // Copy and detach before yielding: another completion can change the ABI's
    // current request while public output waits for its reader.
    try {
      if (kind !== 23) await waitOutputReady(context.signal);
      return await this.dispatch(instance, request, kind, payload, data, context, key, record, rawRequest);
    } catch (error) {
      const expired = cleanupMS !== 0 && context.signal?.aborted;
      return this.completeFailure(instance, request, expired ? 'FINALIZE_TIMEOUT' : error.code ?? 'HOST_FAIL', expired ? 'cleanup deadline expired' : error.message ?? 'host request failed');
    } finally {
      clearTimeout(cleanupTimer);
      if (notifyCancellation) bodySignal.removeEventListener('abort', notifyCancellation);
      if ([3, 13, 14, 15, 16, 32, 33, 34, 35, 36, 37, 38].includes(kind)) this.processGroups.delete(`${instance}:${rawRequest}`);
    }
  }

  async dispatch(instance, request, kind, payload, data, context, key, record, rawRequest) {
    const grants = context.grants;
    if (kind === 26) return this.pluginCallback(instance, request, payload, context);
    if (context.hostRequest && [1, 2, 3, 4, 5, 6, 7, 13, 14, 15, 16, 18, 32, 33, 34, 35, 36, 37, 38].includes(kind)) {
      const response = await this.customHostRequest(instance, request, kind, payload, data, context, key, record);
      if (response !== undefined) return response;
    }
    if (kind === 24) {
      const owner = this.cacheLeaseKey(instance, key);
      if (!this.cacheLeases.has(owner)) {
        // Publish acquisition before yielding, so unlock/cleanup also waits for
        // an acquisition racing cancellation and duplicate acquire requests.
        this.cacheLeases.set(owner, acquireCacheLock(key, context));
      }
      try { await this.cacheLeases.get(owner); }
      catch (error) { return this.completeFailure(instance, request, error.code ?? 'FS_ERR', `cannot acquire cache lock: ${error.message}`); }
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 25) {
      await this.releaseCacheLease(instance, key);
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 22) {
      const milliseconds = Number(payload);
      if (!Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds > 60000) return this.completeFailure(instance, request, 'EXPR_INVALID', 'invalid service timer');
      if (context.signal?.aborted) return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'invocation cancelled');
      await new Promise((resolveTimer) => {
        let timer;
        const cancelled = () => { clearTimeout(timer); resolveTimer(); };
        timer = setTimeout(() => { context.signal?.removeEventListener('abort', cancelled); resolveTimer(); }, milliseconds);
        context.signal?.addEventListener('abort', cancelled, { once: true });
      });
      if (context.signal?.aborted) return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'invocation cancelled');
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 23) {
      let processRequest;
      let graceMS;
      try {
        const cancellation = JSON.parse(payload);
        processRequest = BigInt(cancellation.id);
        graceMS = cancellation.graceMS;
      } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid process cancellation request'); }
      if (!Number.isSafeInteger(graceMS) || graceMS < 0 || graceMS > 60000) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid process cancellation grace period');
      await cancelProcessGroup(this.processGroups.get(`${instance}:${processRequest}`), graceMS);
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 27 || kind === 28 || kind === 29) {
      if (kind === 27 && !grants.read) return this.deny(instance, request, 'read');
      let info;
      try { info = await statResource(payload); }
      catch (error) {
        if (error.code === 'ENOENT' || error.code === 'ENOTDIR') return this.exports.kame_wasm_complete_nil(instance, request);
        if (kind === 27) {
          try { if ((await statResource(payload, undefined, true)).isSymbolicLink()) return this.completeJSON(instance, request, false); } catch {}
        }
        return this.completeFailure(instance, request, 'FS_ERR', 'cannot stat file');
      }
      if (!info.isFile()) return this.completeJSON(instance, request, false);
      // Transport bytes, not a host-chosen freshness decision or metadata hash.
      let bytes;
      try { bytes = await readResource(payload); }
      catch { return this.completeJSON(instance, request, true); }
      const pointer = this.allocate(bytes.length || 1);
      new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
      return this.exports.kame_wasm_complete_bytes(instance, request, pointer, bytes.length);
    }
    if (kind === 19) return this.completeJSON(instance, request, resourceExists(payload));
    if (kind === 18) {
      const resolved = resolveTool(payload, [], context.toolCache);
      if (!resolved) return this.completeFailure(instance, request, 'TOOL_MISSING', `cannot resolve tool: ${payload}`);
      return this.completeJSON(instance, request, resolved);
    }
    if (kind === 1) {
      if (!grants.read) return this.deny(instance, request, 'read');
      try {
        const bytes = await readResource(payload);
        const pointer = this.allocate(bytes.length || 1);
        new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
        return this.exports.kame_wasm_complete_bytes(instance, request, pointer, bytes.length);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', context.inspection ? 'cannot read file' : `cannot read file: ${error.message}`);
      }
    }
    if (kind === 5 || kind === 30) {
      if (!grants.read) return this.deny(instance, request, 'read');
      try {
        const info = await statResource(payload, undefined, kind === 30);
        const result = { name: payload, size: info.size, mode: info.mode, dir: info.isDirectory() };
        if (kind === 30) { result.link = info.isSymbolicLink(); result.regular = info.isFile(); }
        return this.completeJSON(instance, request, result);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', context.inspection ? 'cannot stat file' : `cannot stat file: ${error.message}`);
      }
    }
    if (kind === 31) {
      if (!grants.read) return this.deny(instance, request, 'read');
      try {
        if (isMemoryResource(payload)) throw new Error('memory resources have no links');
        const bytes = await readlink(filesystemPath(payload), { encoding: 'buffer' });
        if (bytes.length >= 65536) throw new Error('link target too large');
        const target = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
        return this.completeJSON(instance, request, target);
      } catch { return this.completeFailure(instance, request, 'FS_ERR', 'cannot read symbolic link'); }
    }
    if (kind === 20) {
      let paths;
      try { paths = JSON.parse(payload); } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid file metadata request'); }
      if (!Array.isArray(paths) || paths.some((name) => typeof name !== 'string' || name.includes('\0'))) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid file metadata paths');
      const times = [];
      for (const name of paths) {
        try { times.push((await statResource(name, { bigint: true })).mtimeNs.toString()); }
        catch { times.push(null); }
      }
      return this.completeJSON(instance, request, times);
    }
    if (kind === 7 || kind === 17) {
      if (kind === 7 && !grants.read) return this.deny(instance, request, 'read');
      try {
        await statResource(payload);
        return this.completeJSON(instance, request, true);
      } catch (error) {
        if (error.code === 'ENOENT' || error.code === 'ENOTDIR') return this.completeJSON(instance, request, false);
        return this.completeFailure(instance, request, 'FS_ERR', 'cannot stat file');
      }
    }
    if (kind === 6) {
      if (!grants.read) return this.deny(instance, request, 'read');
      try { return this.completeJSON(instance, request, resourceWildcard(payload)); }
      catch { return this.completeFailure(instance, request, 'FS_ERR', 'cannot traverse wildcard path'); }
    }
    if (kind === 2) {
      if (!grants.write) return this.deny(instance, request);
      try {
        await writeResource(payload, data);
        return this.exports.kame_wasm_complete_nil(instance, request);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot write file: ${error.message}`);
      }
    }
    if (kind === 4) {
      if (!grants.env) return this.deny(instance, request);
      let value = env[payload];
      for (const entry of context.environment ?? []) {
        const at = entry.indexOf('=');
        if (at > 0 && entry.slice(0, at) === payload) value = entry.slice(at + 1);
      }
      if (value === undefined) return this.exports.kame_wasm_complete_nil(instance, request);
      const encoded = this.write(value);
      return this.exports.kame_wasm_complete_text(instance, request, encoded.pointer, encoded.length);
    }
    if (kind === 13 || kind === 14 || kind === 15 || kind === 32 || kind === 33 || kind === 34 || kind === 35 || kind === 36 || kind === 37 || kind === 38) {
      if (!grants.run) return this.deny(instance, request);
      const decoded = JSON.parse(payload);
      const serviceReadyProbe = kind === 15 && decoded.serviceReady === true;
      const stages = kind === 13 ? [decoded] : kind === 14 ? decoded : decoded.stages;
      const redirections = kind === 15 || kind === 32 || kind === 33 || kind === 34 || kind === 35 || kind === 36 || kind === 37 || kind === 38 ? decoded : { input: '', output: '', append: false };
      redirections.wireKind = kind;
      for (const [field, capability] of [['input', 'read'], ['output', 'write']]) {
        const name = redirections[field];
        if (typeof name !== 'string' || name.includes('\0')) return this.completeFailure(instance, request, 'EXPR_INVALID', 'invalid redirection path');
        if (name && (!grants[capability] || !pathGranted(resolve(name), context[`${capability}Roots`]))) return this.deny(instance, request);
        for (const setup of redirections.setup ?? []) {
          const path = setup[field] ?? '';
          if (typeof path !== 'string' || path.includes('\0')) return this.completeFailure(instance, request, 'EXPR_INVALID', 'invalid stage redirection path');
          if (path && (!grants[capability] || !pathGranted(resolve(path), context[`${capability}Roots`]))) return this.deny(instance, request);
        }
      }
      if (context.runRoots !== null && context.runRoots !== undefined) {
        for (const args of stages) {
          if (args.length === 0) continue; // Native operators have no executable.
          if (!args[0].includes('/')) return this.deny(instance, request);
          const executable = resolve(args[0]);
          if (!context.runRoots.some((root) => executable === root || executable.startsWith(`${root}/`))) return this.deny(instance, request);
        }
      }
      const detached = context.concurrent === true;
      redirections.streamItem = (node, chunk) => {
        if (!this.exports.kame_wasm_stream_item) return -124;
        const pointer = this.scratch('streamItem', chunk.length || 1);
        new Uint8Array(this.exports.memory.buffer, pointer, chunk.length).set(chunk);
        return this.exports.kame_wasm_stream_item(instance, request, node, pointer, chunk.length);
      };
      const onSnapshot = this.exports.kame_wasm_process_snapshot && [32, 33, 34, 35, 36, 37, 38].includes(kind) ? (value) => {
        const data = new TextEncoder().encode(JSON.stringify(value));
        if (data.length > 1048576) return; // Progress is optional, never fail a large graph for inspection.
        const pointer = this.scratch('processSnapshot', data.length || 1);
        new Uint8Array(this.exports.memory.buffer, pointer, data.length).set(data);
        const status = this.exports.kame_wasm_process_snapshot(instance, request, pointer, data.length);
        if (status === 0) this.drainEvents(instance, context);
      } : null;
      const completion = await runArgvCapture(stages, { ...context, ...redirections, onSnapshot, discardStdout: serviceReadyProbe, onStarted: () => { this.processStarted(instance, detached ? request : undefined); return this.startedProcessGroup(instance, context, rawRequest); }, onStdout: redirections.stream ? (chunk) => { this.processStream(instance, false, chunk, detached ? request : undefined); this.drainEvents(instance, context); } : null, onStderr: serviceReadyProbe ? () => {} : context.streaming ? (chunk) => { this.processStream(instance, true, chunk, detached ? request : undefined); this.drainEvents(instance, context); } : null });
      if (completion.ok) {
        if (!redirections.stream && completion.value.status === 0 && completion.value.signal === 0) {
          const encoded = this.write(completion.value.stdout);
          const status = this.exports.kame_wasm_complete_text(instance, request, encoded.pointer, encoded.length);
          if (status !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
          this.drainEvents(instance, context);
          return status;
        }
        return this.completeJSON(instance, request, completion.value);
      }
      return this.completeFailure(instance, request, completion.code, completion.message);
    }
    if (kind === 21) {
      let recipe;
      try { recipe = JSON.parse(payload); } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid output preparation request'); }
      if (!Array.isArray(recipe.outputs) || recipe.outputs.some((name) => typeof name !== 'string' || name.includes('\0'))) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid recipe outputs');
      try { for (const name of recipe.outputs) await mkdir(dirname(name), { recursive: true }); }
      catch { return this.completeFailure(instance, request, 'FS_ERR', 'cannot create output directory'); }
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 3 || kind === 16) {
      if (!grants.run) return this.deny(instance, request);
      if (kind === 16) {
        let recipe;
        try { recipe = JSON.parse(payload); } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid recipe request'); }
        if (typeof recipe.script !== 'string' || !Array.isArray(recipe.outputs) || recipe.outputs.some((name) => typeof name !== 'string' || name.includes('\0'))) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid recipe outputs');
        try { for (const name of recipe.outputs) await mkdir(dirname(name), { recursive: true }); }
        catch { return this.completeFailure(instance, request, 'FS_ERR', 'cannot create output directory'); }
        payload = recipe.script;
        if (recipe.shell !== undefined) {
          if (!Array.isArray(recipe.shell) || recipe.shell.length === 0 || recipe.shell.some((argument) => typeof argument !== 'string' || argument.includes('\0')) || recipe.shell[0] === '') return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid recipe shell argv');
          const defaultPosixShell = recipe.shell.length === 2 && recipe.shell[0] === '/bin/sh' && recipe.shell[1] === '-c';
          const shell = defaultPosixShell ? context.shell.length !== 0 ? context.shell : defaultShell() : recipe.shell;
          context = { ...context, shell };
        }
        if (recipe.environment !== undefined) {
          if (!Array.isArray(recipe.environment) || recipe.environment.some((entry) => typeof entry !== 'string' || entry.includes('\0') || entry.indexOf('=') < 1)) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid recipe environment');
          context = { ...context, environment: recipe.environment, environmentExact: true };
        }
      }
      if (context.streaming) {
        const detached = context.concurrent === true;
        await runProcess(this, instance, payload, context, detached ? request : undefined, rawRequest);
        return 0;
      }
      const completion = await runProcessDirect(payload, context);
      if (completion.ok) {
        const encoded = this.write(completion.text);
        return this.exports.kame_wasm_complete_text(instance, request, encoded.pointer, encoded.length);
      }
      return this.completeFailure(instance, request, completion.code, completion.message);
    }
    if (kind === 8) {
      return this.completeInt(instance, request, BigInt(Date.now()) * 1000000n);
    }
    if (kind === 9) {
      return this.completeInt(instance, request, process.hrtime.bigint());
    }
    if (kind === 10) {
      let bytes;
      try {
        bytes = readFileSync(cacheEntryPath(key));
      } catch {
        return this.exports.kame_wasm_complete_nil(instance, request);
      }
      const pointer = this.allocate(bytes.length || 1);
      new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
      return this.exports.kame_wasm_complete_bytes(instance, request, pointer, bytes.length);
    }
    if (kind === 11) {
      const owner = this.cacheLeaseKey(instance, key);
      const writing = (async () => {
        const path = cacheEntryPath(key);
        await writeFileAtomic(path, record, true);
        pruneCacheDirectory(dirname(path));
      })();
      this.cacheWrites.set(owner, writing);
      try {
        await writing;
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot write cache record: ${error.message}`);
      } finally {
        if (this.cacheWrites.get(owner) === writing) this.cacheWrites.delete(owner);
      }
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    if (kind === 12) {
      try {
        rmSync(cacheEntryPath(key), { force: true });
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot delete cache record: ${error.message}`);
      }
      return this.exports.kame_wasm_complete_nil(instance, request);
    }
    return this.completeFailure(instance, request, 'FEATURE_UNSUP', `host request kind ${kind} is not implemented in this stage`);
  }

  registerPlugins(instance, context) {
    const declarations = context.plugins ?? [];
    if (!Array.isArray(declarations) || declarations.length === 0) return;
    let encoded;
    try { encoded = JSON.stringify(declarations); }
    catch { throw Object.assign(new Error('plugin declarations must be JSON serializable'), { code: 'PLUGIN_CONFIG' }); }
    const bytes = this.write(encoded);
    if (this.exports.kame_wasm_register_plugins(instance, bytes.pointer, bytes.length) !== 0) throw this.compileFailure(instance, 'PLUGIN_CONFIG');
  }

  async pluginCallback(instance, request, payload, context) {
    if (typeof payload === 'string') {
      try { payload = JSON.parse(payload); }
      catch { return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin request'); }
    }
    const identityFields = ['plugin', 'pluginVersion', 'operation', 'operationVersion'];
    if (!payload || typeof payload !== 'object' || identityFields.some((field) => typeof payload[field] !== 'string') || !Number.isSafeInteger(payload.generation) || !Number.isSafeInteger(payload.attempt) || !Array.isArray(payload.argsJSON)) {
      return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin request');
    }
    const callbacks = context.pluginCallbacks;
    const callback = callbacks instanceof Map ? callbacks.get(payload.plugin) : callbacks?.[payload.plugin];
    const declaration = context.plugins.find((plugin) => plugin?.name === payload.plugin);
    const executable = declaration?.argv;
    if (typeof callback !== 'function' && (!Array.isArray(executable) || executable.length === 0 || executable.some((part) => typeof part !== 'string' || part.length === 0))) return this.completeFailure(instance, request, 'FEATURE_UNSUP', 'plugin adapter is unavailable');
    const maxRequestBytes = payload.maxRequestBytes;
    const maxResponseBytes = payload.maxResponseBytes;
    const timeoutMS = payload.timeoutMS;
    if (![maxRequestBytes, maxResponseBytes, timeoutMS].every(Number.isSafeInteger) || maxRequestBytes < 1 || maxResponseBytes < 1 || timeoutMS < 1 || timeoutMS > 30000) {
      return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin limits');
    }
    let args;
    try {
      args = payload.argsJSON.map((item) => {
        if (typeof item !== 'string') throw new Error();
        return JSON.parse(item);
      });
    } catch { return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin arguments'); }
    const invocation = Object.freeze({
      protocol: 1,
      request: request.toString(),
      plugin: payload.plugin,
      pluginVersion: payload.pluginVersion,
      operation: payload.operation,
      operationVersion: payload.operationVersion,
      generation: payload.generation,
      attempt: payload.attempt,
      args,
    });
    if (new TextEncoder().encode(JSON.stringify(invocation)).length > maxRequestBytes) return this.completeFailure(instance, request, 'PLUGIN_LIMIT', 'plugin request exceeds its byte limit');
    if (context.signal?.aborted) return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'plugin call cancelled');
    let timer;
    let abortListener;
    const nativeProcess = typeof callback !== 'function';
    const pluginController = new AbortController();
    const timeout = new Promise((resolveTimeout) => {
      timer = setTimeout(() => {
        stopReason = 'PLUGIN_TIMEOUT';
        pluginController.abort();
        if (!nativeProcess) resolveTimeout({ failure: 'PLUGIN_TIMEOUT' });
      }, timeoutMS);
    });
    let stopReason = '';
    const aborted = new Promise((resolveAbort) => {
      if (!context.signal) return;
      abortListener = () => {
        stopReason = 'EXEC_CANCELLED';
        pluginController.abort();
        if (!nativeProcess) resolveAbort({ failure: 'EXEC_CANCELLED' });
      };
      context.signal.addEventListener('abort', abortListener, { once: true });
    });
    let response;
    try {
      const invoke = typeof callback === 'function'
        ? Promise.resolve().then(() => callback(invocation, { signal: pluginController.signal }))
        : runPluginProcess(invocation, executable, maxResponseBytes, pluginController.signal, context.cwd, context.finalizing);
      response = await Promise.race([invoke, timeout, aborted]);
    }
    catch (error) {
      clearTimeout(timer);
      if (abortListener) context.signal.removeEventListener('abort', abortListener);
      pluginController.abort();
      if (stopReason === 'PLUGIN_TIMEOUT') return this.completeFailure(instance, request, 'PLUGIN_TIMEOUT', 'plugin adapter timed out');
      if (stopReason === 'EXEC_CANCELLED') return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'plugin call cancelled');
      const code = error?.code === 'PLUGIN_LIMIT' || error?.code === 'PLUGIN_PROTOCOL' ? error.code : 'PLUGIN_FAIL';
      const message = code === 'PLUGIN_LIMIT' ? 'plugin result exceeds its byte limit' : code === 'PLUGIN_PROTOCOL' ? 'invalid plugin response' : 'plugin adapter failed';
      return this.completeFailure(instance, request, code, message);
    }
    clearTimeout(timer);
    if (abortListener) context.signal.removeEventListener('abort', abortListener);
    if (response?.failure === 'PLUGIN_TIMEOUT') { pluginController.abort(); return this.completeFailure(instance, request, 'PLUGIN_TIMEOUT', 'plugin callback timed out'); }
    if (response?.failure === 'EXEC_CANCELLED' || context.signal?.aborted) { pluginController.abort(); return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'plugin call cancelled'); }
    pluginController.abort();
    const matches = response && response.protocol === 1 && response.request === invocation.request && response.plugin === invocation.plugin && response.pluginVersion === invocation.pluginVersion && response.operation === invocation.operation && response.operationVersion === invocation.operationVersion && response.generation === invocation.generation && response.attempt === invocation.attempt;
    if (!matches) return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'plugin response identity mismatch');
    const allowed = new Set(['protocol', 'request', 'plugin', 'pluginVersion', 'operation', 'operationVersion', 'generation', 'attempt', 'value', 'error']);
    if (Object.keys(response).some((field) => !allowed.has(field)) || (Object.hasOwn(response, 'value') === Object.hasOwn(response, 'error'))) return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin response fields');
    let encodedEnvelope;
    try { encodedEnvelope = JSON.stringify(response); }
    catch { return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin response'); }
    if (new TextEncoder().encode(encodedEnvelope).length > maxResponseBytes) return this.completeFailure(instance, request, 'PLUGIN_LIMIT', 'plugin result exceeds its byte limit');
    if (Object.hasOwn(response, 'error')) {
      const error = response.error;
      if (!error || typeof error !== 'object' || Array.isArray(error) || Object.keys(error).length !== 2 || Object.keys(error).some((field) => !['code', 'message'].includes(field)) || typeof error.code !== 'string' || !/^[A-Z][A-Z0-9_]{0,63}$/.test(error.code) || typeof error.message !== 'string' || new TextEncoder().encode(error.message).length > 256) return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin error response');
      return this.completeFailure(instance, request, error.code, error.message);
    }
    let encoded;
    try { encoded = JSON.stringify(response.value); }
    catch { return this.completeFailure(instance, request, 'PLUGIN_PROTOCOL', 'invalid plugin result'); }
    const bytes = new TextEncoder().encode(encoded);
    const pointer = this.allocate(bytes.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
    return this.exports.kame_wasm_complete_text(instance, request, pointer, bytes.length);
  }

  async customHostRequest(instance, request, kind, payload, data, context, key, record) {
    const capability = kind === 1 || kind === 5 || kind === 6 || kind === 7 || kind === 20 || kind === 27 || kind === 30 || kind === 31 ? 'read'
      : kind === 2 ? 'write'
        : kind === 4 ? 'env'
          : kind === 3 || kind === 13 || kind === 14 || kind === 15 || kind === 16 || kind === 32 || kind === 33 || kind === 34 || kind === 35 || kind === 36 || kind === 37 || kind === 38 ? 'run' : null;
    if (capability && !context.grants[capability]) return this.deny(instance, request, capability);
    if ((kind === 1 || kind === 5 || kind === 6 || kind === 7 || kind === 20 || kind === 27 || kind === 30 || kind === 31 || kind === 2) && typeof payload === 'string') {
      const grantKind = kind === 2 ? 'write' : 'read';
      const roots = context[`${grantKind}Roots`];
      let names = [kind === 6 ? payload.replace(/\\(.)/gs, '$1') : payload];
      if (kind === 20) {
        try { names = JSON.parse(payload); } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid file metadata paths'); }
        if (!Array.isArray(names) || names.some((name) => typeof name !== 'string')) return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid file metadata paths');
      }
      if (roots !== null && roots !== undefined && names.some((name) => !pathGranted(resolve(context.cwd ?? process.cwd(), name), roots))) return this.deny(instance, request, grantKind);
    }
    if (capability === 'run' && context.runRoots !== null && context.runRoots !== undefined && [13, 14, 15, 32, 33, 34, 35, 36, 37, 38].includes(kind)) {
      let stages, decoded;
      try {
        decoded = JSON.parse(payload);
        stages = kind === 13 ? [decoded] : kind === 14 ? decoded : decoded.stages;
      } catch { return this.completeFailure(instance, request, 'HOST_FAIL', 'invalid process request'); }
      if (!Array.isArray(stages) || stages.some((args, i) => {
        if ((kind === 32 || kind === 33 || kind === 34 || kind === 35 || kind === 36 || kind === 37 || kind === 38) && Array.isArray(args) && args.length === 0 && ['merge', 'broadcast', 'balance', 'frame', 'encode', 'filter', 'copy'].includes(decoded.kinds?.[i])) return false;
        return !Array.isArray(args) || typeof args[0] !== 'string' || !args[0].includes('/') || !pathGranted(resolve(context.cwd ?? process.cwd(), args[0]), context.runRoots);
      })) return this.deny(instance, request, 'run');
    }
    if (context.signal?.aborted) return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'invocation cancelled');
    const descriptor = Object.freeze({
      id: request,
      kind,
      payload,
      data: data?.slice() ?? new Uint8Array(0),
      key: key?.slice() ?? null,
      record: record?.slice() ?? null,
      capability,
      signal: context.signal,
    });
    let abortListener;
    const aborted = new Promise((resolveAbort) => {
      if (!context.signal) return;
      abortListener = () => resolveAbort({ type: 'failure', code: 'EXEC_CANCELLED', message: 'invocation cancelled' });
      context.signal.addEventListener('abort', abortListener, { once: true });
    });
    let result;
    try {
      const work = Promise.resolve().then(() => context.hostRequest(descriptor));
      // With an armed finalizer, settling the callback is its acknowledgement
      // that host work has stopped. Racing abort would let cleanup touch a
      // resource while an abandoned callback still uses it.
      result = await (context.signal && !context.finalizationActive ? Promise.race([work, aborted]) : work);
    } catch (error) {
      return this.completeFailure(instance, request, error.code ?? 'HOST_FAIL', error.message ?? 'host request failed');
    } finally {
      if (abortListener) context.signal.removeEventListener('abort', abortListener);
    }
    if (context.signal?.aborted) return this.completeFailure(instance, request, 'EXEC_CANCELLED', 'invocation cancelled');
    if (!result || typeof result !== 'object') return this.completeFailure(instance, request, 'HOST_FAIL', 'host callback returned no completion');
    if (result.type === 'bytes' && (result.value instanceof Uint8Array || Buffer.isBuffer(result.value))) {
      const bytes = this.write(result.value);
      return this.exports.kame_wasm_complete_bytes(instance, request, bytes.pointer, bytes.length);
    }
    if (result.type === 'text' && typeof result.value === 'string') {
      const bytes = this.write(result.value);
      return this.exports.kame_wasm_complete_text(instance, request, bytes.pointer, bytes.length);
    }
    if (result.type === 'json') return this.completeJSON(instance, request, result.value);
    if (result.type === 'nil') return this.exports.kame_wasm_complete_nil(instance, request);
    if (result.type === 'failure' && typeof result.code === 'string' && typeof result.message === 'string') {
      return this.completeFailure(instance, request, result.code, result.message);
    }
    return this.completeFailure(instance, request, 'HOST_FAIL', 'host callback returned an invalid completion');
  }

  cacheLeaseKey(instance, key) {
    return `${instance}:${Buffer.from(key).toString('hex')}`;
  }

  async releaseCacheLease(instance, key) {
    const owner = this.cacheLeaseKey(instance, key);
    await this.releaseCacheOwner(owner);
  }

  async releaseCacheOwner(owner) {
    const writing = this.cacheWrites.get(owner);
    if (writing) { try { await writing; } catch {} }
    const acquired = this.cacheLeases.get(owner);
    if (!acquired) return;
    this.cacheLeases.delete(owner);
    let release;
    try { release = await acquired; } catch { return; }
    await release();
  }

  step(instance, context) {
    const cancelling = context?.signal?.aborted === true;
    const finalizing = this.exports.kame_wasm_finalization_tick?.(instance, process.hrtime.bigint(), cancelling ? 1 : 0);
    if (cancelling && finalizing !== 1) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
    return this.exports.kame_wasm_step(instance);
  }

  async waitReady(instance, context) {
    this.exports.kame_wasm_finalization_tick?.(instance, process.hrtime.bigint(), 0);
    const remaining = Number(this.exports.kame_wasm_finalization_budget?.(instance, 1) ?? 0n);
    if (remaining < 0) return;
    if (remaining === 0) return waitOutputReady(context.signal);
    const deadline = new AbortController();
    const abort = () => deadline.abort();
    context.signal?.addEventListener('abort', abort, { once: true });
    if (context.signal?.aborted) abort();
    const timer = setTimeout(abort, remaining);
    try { await waitOutputReady(deadline.signal); }
    finally { clearTimeout(timer); context.signal?.removeEventListener('abort', abort); }
  }

  async cleanupInstance(instance, context) {
    if (context && this.exports.kame_wasm_finalization_tick) {
      const pending = new Set();
      const stopping = { ...context, concurrent: true, signal: AbortSignal.abort() };
      while (this.exports.kame_wasm_finalization_tick(instance, process.hrtime.bigint(), 1) === 1) {
        const state = this.step(instance);
        this.drainEvents(instance, stopping);
        this.drainExpressionEffects(instance);
        if (state === 1) {
          const work = this.service(instance, stopping).finally(() => pending.delete(work));
          pending.add(work);
        } else {
          await Promise.race([...pending, new Promise(resolve => setTimeout(resolve, 5))]);
        }
      }
      await Promise.all(pending);
      this.drainEvents(instance, stopping);
      this.drainExpressionEffects(instance);
    }
    const prefix = `${instance}:`;
    await Promise.all([...this.cacheWrites].filter(([owner]) => owner.startsWith(prefix)).map(([, writing]) => writing.catch(() => {})));
    await Promise.all([...this.cacheLeases.keys()].filter((owner) => owner.startsWith(prefix)).map((owner) => this.releaseCacheOwner(owner)));
    for (const owner of this.processGroups.keys()) if (owner.startsWith(prefix)) this.processGroups.delete(owner);
    return this.resultDiagnostic(instance);
  }

  resultDiagnostic(instance) {
    if (!this.exports.kame_wasm_result_diagnostic) return undefined;
    try {
      const bytes = this.copyQuery((handle, dst, capacity, length) => this.exports.kame_wasm_result_diagnostic(handle, dst, capacity, length), instance);
      return bytes.length ? JSON.parse(this.decode0(bytes)).diagnostic : undefined;
    } catch (error) {
      // Fatal allocator exhaustion may prevent diagnostic copying; it must not
      // replace an already established invocation failure or prevent disposal.
      if (error.code === 'NO_MEMORY') return undefined;
      throw error;
    }
  }

  startedProcessGroup(instance, context, rawRequest) {
    this.drainEvents(instance, context);
    if (rawRequest === undefined) return undefined;
    const owner = `${instance}:${rawRequest}`;
    let group = this.processGroups.get(owner);
    if (!group) this.processGroups.set(owner, group = new Set());
    return group;
  }

  deny(instance, request, capability) {
    const message = capability === 'read' ? 'read access denied; grant the required path with --allow-read=ROOT' : 'operation capability denied';
    return this.completeFailure(instance, request, 'CAP_DENIED', message);
  }

  async evaluate(source, expression, context) {
    if (context.signal?.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    let failure, primaryDiagnostic;
    try {
      const compiled = this.write(source);
      if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      this.registerPlugins(instance, context);
      this.inspectionGrant(instance, '', '');
      for (const grant of context.portableGrants ?? []) {
        if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
        else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
      }
      if (context.cwd) {
        const directory = this.write(context.cwd);
        if (this.exports.kame_wasm_set_directory(instance, directory.pointer, directory.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      }
      const encoded = this.write(expression);
      if (this.exports.kame_wasm_expression_begin(instance, encoded.pointer, encoded.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'PARSE_ERR');
      for (;;) {
        await this.waitReady(instance, context);
        if (context.signal?.aborted) {
          throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
        }
        const state = this.step(instance, context);
        this.drainExpressionEffects(instance);
        if (state === 2) break;
        if (state !== 1) continue;
        await this.service(instance, context);
      }
      return this.copyResult(instance);
    } catch (error) {
      failure = error;
      primaryDiagnostic = lastDiagnostic;
      throw error;
    } finally {
      try {
        if (context.signal?.aborted) this.exports.kame_wasm_expression_cancel(instance);
        const diagnostic = await this.cleanupInstance(instance, context);
        if (failure && diagnostic) failure.diagnostics = [diagnostic];
      } finally {
        this.exports.kame_wasm_instance_free(instance);
        if (failure) lastDiagnostic = primaryDiagnostic;
      }
    }
  }

  drainExpressionEffects(instance) {
    for (;;) {
      const kind = this.exports.kame_wasm_expression_effect_kind(instance);
      if (kind === 0) return;
      const length = this.exports.kame_wasm_expression_effect_length(instance);
      const pointer = this.allocate(length || 1);
      if (this.exports.kame_wasm_expression_effect_copy(instance, pointer, length) !== 0) throw new Error('expression effect copy failed');
      const data = new Uint8Array(this.exports.memory.buffer, pointer, length).slice();
      if (kind === 2) stderr.write(data); else stdout.write(data);
    }
  }

  async parse(lang, name, text) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const langBytes = this.write(lang);
      const nameBytes = this.write(name);
      const textBytes = this.write(text);
      const call = (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_parse(handle, langBytes.pointer, langBytes.length, nameBytes.pointer, nameBytes.length, textBytes.pointer, textBytes.length, dst, dstLen, lengthPointer);
      return this.copyQuery(call, instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  async format(lang, name, text, indentStyle, indentWidth) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const langBytes = this.write(lang);
      const nameBytes = this.write(name);
      const textBytes = this.write(text);
      const indentBytes = this.write(indentStyle);
      const call = (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_format(handle, langBytes.pointer, langBytes.length, nameBytes.pointer, nameBytes.length, textBytes.pointer, textBytes.length, indentBytes.pointer, indentBytes.length, indentWidth, dst, dstLen, lengthPointer);
      return this.copyQuery(call, instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  async prepared(source, run, name, context, resolveTools = true) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      this.compileBuild(instance, source, name);
      if (context) {
        const directory = this.write(process.cwd());
        if (this.exports.kame_wasm_set_directory(instance, directory.pointer, directory.length) !== 0 || this.exports.kame_wasm_set_forwarding(instance, 1) !== 0) throw Object.assign(new Error('inspection host forwarding unavailable'), { code: 'HOST_FAIL' });
        this.inspectionGrant(instance, '', '');
        for (const grant of context.inspectionGrants) {
          if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
          else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
        }
      }
      if (this.exports.kame_wasm_prepare(instance) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      if (resolveTools) {
        const names = JSON.parse(this.decode0(this.copyQuery((handle, dst, capacity, length) => this.exports.kame_wasm_tools(handle, dst, capacity, length), instance)));
        for (const tool of names) this.setToolPath(instance, tool, resolveTool(tool, Array.isArray(source) ? source.toolOverrides : [], context?.toolCache));
      }
      if (context) return await this.copyInspectionQuery(run(instance), instance, context);
      return this.copyQuery(run(instance), instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  compileBuild(instance, source, name, context = {}) {
    if (name) this.setSourceName(instance, name);
    const compiled = this.write(Array.isArray(source) ? '' : source);
    if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
    if (Array.isArray(source)) {
      if (!this.exports.kame_wasm_set_build_sources) throw Object.assign(new Error('include source ABI unavailable'), { code: 'FEATURE_UNSUP' });
      const descriptor = this.write(JSON.stringify({ sources: source, defines: source.defines, parameters: source.parameters, environment: source.environment, toolOverrides: source.toolOverrides, force: source.force, timeoutMS: context.timeoutMS ?? 0, retryCount: context.retryCount ?? 0, retainBytes: context.logLimit ?? 0 }));
      if (this.exports.kame_wasm_set_build_sources(instance, descriptor.pointer, descriptor.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
    }
  }

  setSourceName(instance, name) {
    const bytes = this.write(name);
    if (this.exports.kame_wasm_set_source_name(instance, bytes.pointer, bytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  async targetOperands(source, targets, name) {
    const bytes = await this.prepared(source, () => {
      const operands = this.write(JSON.stringify(targets));
      return (handle, dst, capacity, length) => this.exports.kame_wasm_target_operands(handle, operands.pointer, operands.length, dst, capacity, length);
    }, name, undefined, false);
    return JSON.parse(this.decode0(bytes));
  }

  async planJSON(source, target, expand, name, context) {
    return this.prepared(source, (instance) => {
      const targetBytes = this.write(target);
      const flag = expand ? 1 : 0;
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_plan(handle, targetBytes.pointer, targetBytes.length, flag, dst, dstLen, lengthPointer);
    }, name, context);
  }

  async graphJSON(source, target, depth, kind, queryOption, name, context) {
    return this.prepared(source, (instance) => {
      const targetBytes = this.write(target);
      // The legacy ABI slot expands span queries or selects affected plans.
      const flag = queryOption ? 1 : 0;
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_graph(handle, targetBytes.pointer, targetBytes.length, depth, kind, flag, dst, dstLen, lengthPointer);
    }, name, context, kind !== graphKinds.list);
  }

  async toolNames(source, name) {
    const bytes = await this.prepared(source, () => {
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_tools(handle, dst, dstLen, lengthPointer);
    }, name, undefined, false);
    return JSON.parse(this.decode0(bytes));
  }

  setToolPath(instance, name, path) {
    const n = this.write(name);
    const p = this.write(path);
    if (this.exports.kame_wasm_set_tool_path(instance, n.pointer, n.length, p.pointer, p.length) !== 0) throw new Error('cannot supply tool path');
  }

  inspectionGrant(instance, capability, name) {
    const c = this.write(capability);
    const n = this.write(name);
    if (this.exports.kame_wasm_inspection_grant(instance, c.pointer, c.length, n.pointer, n.length) !== 0) throw Object.assign(new Error('inspection capability policy unavailable'), { code: 'HOST_FAIL' });
  }

  async toolsCheck(source, target, name, context) {
    return this.prepared(source, (instance) => {
      const t = this.write(target);
      const query = context?.cliReport ? this.exports.kame_wasm_tools_check_report : this.exports.kame_wasm_tools_check;
      if (!query) throw Object.assign(new Error('tool-check presentation query unavailable'), { code: 'FEATURE_UNSUP' });
      return (handle, dst, dstLen, lengthPointer) => query(handle, t.pointer, t.length, dst, dstLen, lengthPointer);
    }, name, context);
  }

  // drainEvents pops every queued target event. JSON mode writes the raw event
  // lines to stdout; a human primary run forwards process streams and progress
  // to stdout/stderr like the native CLI.
  drainEvents(instance, context) {
    const json = context.json === true;
    const human = context.human === true && !json;
    for (;;) {
      const lengthPointer = this.scratch('eventLength', 4, 4);
      const query = this.exports.kame_wasm_target_event(instance, 0, 0, lengthPointer);
      if (query !== 0 && query !== 3) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      const length = new DataView(this.exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
      if (length === 0) { if (context.api !== true) redrawDashboard(); return; }
      const output = this.scratch('eventOutput', length || 1);
      if (this.exports.kame_wasm_target_event(instance, output, length, lengthPointer) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      const bytes = new Uint8Array(this.exports.memory.buffer, output, length).slice();
      for (const line of this.decode0(bytes).trim().split('\n')) {
        const event = JSON.parse(line);
        if (context.render && event.type === 'target-value') {
          context.renderData = Buffer.from(event.value?.data ?? '', event.value?.encoding === 'base64' ? 'base64' : 'utf8');
        }
        if (event.diagnostic && (event.type === 'target-failed' || event.type === 'target-cancelled')) lastDiagnostic = event.diagnostic;
        // Non-JSON expression roots only transport the rich failure; they do not
        // add build lifecycle output or count toward build summaries.
        if (!json && context.api !== true && event.resource?.kind === 'definition') continue;
        if (context.api !== true && (json || human)) observeCLIEvent(event);
        if (context.events) {
          const previous = event.type === 'process-snapshot' ? context.events.findIndex((old) => old.type === event.type && old.request === event.request) : -1;
          if (previous >= 0) context.events[previous] = event;
          else context.events.push(event);
        }
      }
      if (context.api === true) {
        continue;
      } else if (json) {
        if (!context.render || !['target-value', 'target-completed'].includes(JSON.parse(this.decode0(bytes)).type)) stdout.write(bytes);
      } else if (human) {
        humanEvent(bytes);
      }
    }
  }

  processStarted(instance, request) {
    const status = request === undefined ? this.exports.kame_wasm_process_started(instance) : this.exports.kame_wasm_process_started_request(instance, request);
    if (status !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  processExited(instance, request) {
    if (request === undefined) return;
    const status = this.exports.kame_wasm_process_exited_request(instance, request);
    if (status !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  processStream(instance, stderrStream, chunk, request) {
    const pointer = this.scratch('processStream', chunk.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, chunk.length).set(chunk);
    const status = request === undefined
      ? this.exports.kame_wasm_process_stream(instance, stderrStream ? 1 : 0, pointer, chunk.length)
      : this.exports.kame_wasm_process_stream_request(instance, request, stderrStream ? 1 : 0, pointer, chunk.length);
    if (status !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  processTerminal(instance, outcome, status, signal, stdoutBytes, stderrBytes, code, message, request) {
    if (request !== undefined && this.exports.kame_wasm_request_attach(instance, request) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
    const stdout = this.write(stdoutBytes ?? new Uint8Array(0));
    const stderr = this.write(stderrBytes ?? new Uint8Array(0));
    const codeBytes = this.write(code);
    const messageBytes = this.write(message);
    if (this.exports.kame_wasm_process_terminal(instance, status, signal, outcome, stdout.pointer, stdout.length, stderr.pointer, stderr.length, codeBytes.pointer, codeBytes.length, messageBytes.pointer, messageBytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  async materialize(source, target, context, name) {
    const tools = await this.toolNames(source, name);
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    const pending = new Set();
    let disposed;
    const disposal = new Promise(resolve => { disposed = resolve; });
    invocationDisposals.add(disposal);
    const cancellation = new AbortController();
    const externalSignal = context.signal;
    const cancel = () => cancellation.abort();
    externalSignal?.addEventListener('abort', cancel, { once: true });
    invocationCancellations.add(cancellation);
    if (externalSignal?.aborted) cancel();
    context.concurrent = true;
    context.signal = cancellation.signal;
    let hostError, primaryDiagnostic;
    let targetStarted = false;
    let failure;
    try {
      this.compileBuild(instance, source, name, context);
      this.registerPlugins(instance, context);
      for (const tool of tools) this.setToolPath(instance, tool, resolveTool(tool, Array.isArray(source) ? source.toolOverrides : [], context.toolCache));
      const directoryBytes = this.write(context.cwd ?? process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directoryBytes.pointer, directoryBytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      if (this.exports.kame_wasm_set_forwarding(instance, 1) !== 0) throw Object.assign(new Error('request forwarding is unavailable'), { code: 'FEATURE_UNSUP' });
      if (context.portableGrants) {
        this.inspectionGrant(instance, '', '');
        for (const grant of context.portableGrants) {
          if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
          else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
        }
      }
      context.streaming = true;
      const encoded = this.write(target);
      if (this.exports.kame_wasm_target_begin(instance, encoded.pointer, encoded.length) !== 0) throw this.compileFailure(instance, 'TGT_NO_RULE');
      targetStarted = true;
      for (;;) {
        await this.waitReady(instance, context);
        const state = this.step(instance, context);
        this.drainEvents(instance, context);
        if (state === 2) break;
        if (state !== 1) {
          if (hostError) throw hostError;
          if (pending.size) await Promise.race([...pending, new Promise((resolve) => setTimeout(resolve, 10))]);
          continue;
        }
        if (hostError) throw hostError;
        const request = this.service(instance, context).catch((error) => { hostError = error; }).finally(() => pending.delete(request));
        pending.add(request);
        await Promise.resolve();
      }
      const kind = this.exports.kame_wasm_result_kind(instance);
      return { kind, bytes: this.copyResult(instance), events: context.events?.slice() ?? [] };
    } catch (error) {
      failure = error;
      primaryDiagnostic = lastDiagnostic;
      throw error;
    } finally {
      try {
        cancellation.abort();
        externalSignal?.removeEventListener('abort', cancel);
        await Promise.all(pending);
        try {
          if (targetStarted) this.drainEvents(instance, context);
        } finally {
          const diagnostic = await this.cleanupInstance(instance, context);
          if (context.api && failure && diagnostic) failure.diagnostics = [diagnostic];
          invocationCancellations.delete(cancellation);
          this.exports.kame_wasm_instance_free(instance);
        }
      } finally {
        if (failure) lastDiagnostic = primaryDiagnostic;
        invocationDisposals.delete(disposal);
        disposed();
      }
    }
  }

  // ponytail: reuse watch roots (1024-target limit); add a batch ABI only for larger builds.
  async materializeMany(source, targets, context, name) {
    if (context.signal?.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
    if (targets.length === 0) return [];
    if (targets.length === 1) return [await this.materialize(source, targets[0], context, name)];
    const watch = await this.beginWatch(source, targets, name, context);
    invocationCancellations.add(watch.cancellation);
    try {
      for (;;) {
        if (context.signal.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
        await this.waitReady(watch.instance, context);
        const state = this.drainWatch(watch);
        const snapshot = this.watchSnapshot(watch);
        this.drainEvents(watch.instance, context);
        const failed = snapshot.roots.find((root) => root.diagnosticJSON);
        if (failed) {
          const diagnostic = JSON.parse(failed.diagnosticJSON).diagnostic;
          throw Object.assign(new Error(diagnostic.message), { code: diagnostic.code, diagnostics: [diagnostic] });
        }
        if (watch.hostError) throw watch.hostError;
        if (state === 1) {
          // A completed producer may still have its accepted dependency record
          // queued. Persist it before disposal cancels outstanding host work.
          if (!snapshot.busy && this.exports.kame_wasm_next_request_kind(watch.instance) === 11) await this.service(watch.instance, context);
          else this.serviceWatchRequest(watch);
          continue;
        }
        if (!snapshot.busy) return snapshot.roots.map((root) => ({ kind: root.kind, bytes: new TextEncoder().encode(root.value ?? '') }));
        if (watch.pending.size) await Promise.race([...watch.pending, new Promise((resolve) => setTimeout(resolve, 10))]);
      }
    } finally {
      await this.disposeWatch(watch);
      invocationCancellations.delete(watch.cancellation);
    }
  }

  async beginWatch(source, targets, name, context) {
    for (const method of ['kame_wasm_watch_begin', 'kame_wasm_watch_state', 'kame_wasm_watch_invalidate', 'kame_wasm_watch_cancel']) {
      if (!this.exports[method]) throw Object.assign(new Error(`watch ABI is missing ${method}`), { code: 'FEATURE_UNSUP' });
    }
    const tools = await this.toolNames(source, name);
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM watch instance'), { code: 'NO_MEMORY' });
    const pending = new Set();
    const cancellation = new AbortController();
    const externalSignal = context.signal;
    const cancel = () => cancellation.abort();
    externalSignal?.addEventListener('abort', cancel, { once: true });
    context.concurrent = true;
    if (externalSignal?.aborted) cancel();
    context.signal = cancellation.signal;
    context.streaming = true;
    context.human = !context.json;
    let hostError;
    try {
      this.compileBuild(instance, source, name, context);
      this.registerPlugins(instance, context);
      for (const tool of tools) this.setToolPath(instance, tool, resolveTool(tool, Array.isArray(source) ? source.toolOverrides : [], context.toolCache));
      const directory = this.write(context.cwd ?? process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directory.pointer, directory.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      if (this.exports.kame_wasm_set_forwarding(instance, 1) !== 0) throw Object.assign(new Error('request forwarding is unavailable'), { code: 'FEATURE_UNSUP' });
      if (context.portableGrants) {
        this.inspectionGrant(instance, '', '');
        for (const grant of context.portableGrants) {
          if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
          else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
        }
      }
      const targetsBytes = this.write(JSON.stringify(targets));
      if (this.exports.kame_wasm_watch_begin(instance, targetsBytes.pointer, targetsBytes.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      return { instance, pending, cancellation, context, targets, hostError, live: true, externalSignal, cancel };
    } catch (error) {
      cancellation.abort();
      externalSignal?.removeEventListener('abort', cancel);
      await Promise.all(pending);
      await this.cleanupInstance(instance, context);
      this.exports.kame_wasm_instance_free(instance);
      throw error;
    }
  }

  watchSnapshot(watch) {
    return JSON.parse(this.decode0(this.copyQuery((handle, dst, capacity, length) => this.exports.kame_wasm_watch_state(handle, dst, capacity, length), watch.instance)));
  }

  invalidateWatch(watch, resources) {
    const bytes = this.write(JSON.stringify(resources));
    const status = this.exports.kame_wasm_watch_invalidate(watch.instance, bytes.pointer, bytes.length);
    if (status !== 0) throw diagnosticError(this.instanceDiagnostic(watch.instance), 'HOST_FAIL');
  }

  drainWatch(watch) {
    const { instance, context } = watch;
    const state = this.step(instance, context);
    this.drainEvents(instance, context);
    this.drainExpressionEffects(instance);
    return state;
  }

  serviceWatchRequest(watch) {
    const request = this.service(watch.instance, watch.context)
      .catch((error) => { watch.hostError = error; })
      .finally(() => watch.pending.delete(request));
    watch.pending.add(request);
  }

  async disposeWatch(watch) {
    if (!watch?.live) return;
    watch.live = false;
    watch.cancellation.abort();
    await Promise.all(watch.pending);
    this.exports.kame_wasm_watch_cancel(watch.instance);
    await this.cleanupInstance(watch.instance, watch.context);
    this.exports.kame_wasm_instance_free(watch.instance);
    watch.externalSignal?.removeEventListener('abort', watch.cancel);
  }

  copyResult(instance) {
    const call = (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_result_copy(handle, dst, dstLen, lengthPointer);
    return this.copyQuery(call, instance);
  }

  async runSession(fragments, inv, context) {
    if (!this.exports.kame_wasm_session_compile) throw Object.assign(new Error('runner session ABI unavailable'), { code: 'FEATURE_UNSUP' });
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    const pending = new Set();
    let disposed;
    const disposal = new Promise(resolve => { disposed = resolve; });
    invocationDisposals.add(disposal);
    const cancellation = new AbortController();
    invocationCancellations.add(cancellation);
    context.concurrent = true;
    context.signal = cancellation.signal;
    let hostError, primaryError, primaryDiagnostic;
    try {
      if (this.exports.kame_wasm_source_compile(instance, 0, 0) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      this.registerPlugins(instance, context);
      const directory = this.write(process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directory.pointer, directory.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      this.inspectionGrant(instance, '', '');
      const grants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }];
      for (const grant of grants) {
        if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
        else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
      }
      const environment = Object.entries(process.env).map(([name, value]) => `${name}=${value}`);
      environment.push(...inv.environment);
      const descriptor = this.write(JSON.stringify({ fragments, args: inv.args, toolOverrides: inv.toolOverrides, buildDefines: inv.name === 'render' ? [] : inv.defines, parameters: inv.parameters, environment, captureLimit: inv.captureLimit, printResult: inv.printResult ? 1 : 0, json: inv.json ? 1 : 0, dryRun: inv.dryRun ? 1 : 0, force: inv.force ? 1 : 0, timeoutMS: inv.timeoutMS ?? 0, retryCount: inv.retryCount ?? 0, retainBytes: inv.logLimit ?? 0 }));
      if (this.exports.kame_wasm_session_compile(instance, descriptor.pointer, descriptor.length) !== 0) {
        if (inv.json) { this.drainEvents(instance, context); return 1; }
        throw this.compileFailure(instance, 'PARSE_ERR');
      }
      context.streaming = true;
      context.human = !inv.json;
      context.render = inv.name === 'render';
      let last;
      const deadline = inv.timeoutMS > 0 ? performance.now() + inv.timeoutMS : Infinity;
      const count = this.exports.kame_wasm_session_work_count(instance);
      for (let i = 0; i < count; i++) {
        const kind = this.exports.kame_wasm_session_work_kind(instance, i);
        if (this.exports.kame_wasm_session_work_begin(instance, i) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
        for (;;) {
          context.timeoutMS = Number.isFinite(deadline) ? Math.max(1, Math.ceil(deadline - performance.now())) : 0;
          if (performance.now() >= deadline) throw Object.assign(new Error('invocation timed out'), { code: 'RECIPE_TIMEOUT' });
          await this.waitReady(instance, context);
          const state = this.step(instance, context);
          this.drainEvents(instance, context);
          this.drainExpressionEffects(instance);
          if (state === 2) break;
          if (hostError) throw hostError;
          if (state === 1) {
            const request = this.service(instance, context).catch((error) => { hostError = error; }).finally(() => pending.delete(request));
            pending.add(request);
            await Promise.resolve();
          } else if (pending.size) {
            await Promise.race([...pending, new Promise((resolve) => setTimeout(resolve, 10))]);
          }
        }
        const bytes = this.copyResult(instance);
        if (!inv.json && !inv.dryRun && kind === 2) stdout.write(bytes);
        else if (!inv.json && !inv.dryRun && kind === 1) last = bytes;
      }
      if (inv.name === 'render') {
        if (jsonMode) writeDataResult('render-result', { source: fragments[0]?.name ?? '<stdin>', action: inv.check ? 'check' : 'render' }, context.renderData, !inv.check);
        else if (inv.check) stderr.write(`${styled('status.success', 'done')} render check ${fragments[0]?.name ?? '<stdin>'}\n`);
        else if (last !== undefined) rawPublication(stdout, last);
      } else if (last !== undefined) rawPublication(stdout, last);
      return 0;
    } catch (error) {
      if (error.code === 'RECIPE_TIMEOUT') this.exports.kame_wasm_finalization_tick?.(instance, process.hrtime.bigint(), 2);
      primaryError = error;
      primaryDiagnostic = lastDiagnostic;
      if (inv.json && lastDiagnostic !== null) return 1;
      throw error;
    } finally {
      cancellation.abort();
      await Promise.all(pending);
      try {
        try {
          const diagnostic = await this.cleanupInstance(instance, context);
          if (diagnostic && primaryError) primaryError.diagnostics = [{ ...diagnostic, code: primaryError.code, message: primaryError.message }];
          else if (diagnostic && !interruptedStatus) throw Object.assign(new Error(diagnostic.message), { code: diagnostic.code, diagnostics: [diagnostic] });
        }
        catch (error) { if (!primaryError && !interruptedStatus) throw error; reportWatchError(error); }
      } finally {
        invocationCancellations.delete(cancellation);
        this.exports.kame_wasm_instance_free(instance);
        if (primaryError) lastDiagnostic = primaryDiagnostic;
        disposed();
        invocationDisposals.delete(disposal);
      }
    }
  }

  async selfTest() {
    const info = this.abiInfo();
    const context = { grants: { read: true, write: true, run: true, env: true } };
    const pure = this.decode0(await this.evaluate('', '(join ":" ["a" "b"])', context));
    const expression = this.decode0(await this.evaluate('', '(count [1 2 3])', context));
    return { ...info, selfTest: { pure, expression } };
  }

  decode0(bytes) {
    return new TextDecoder().decode(bytes);
  }
}

function terminatePluginChild(child) {
  if (!child || child.pid === undefined) return undefined;
  if (process.platform === 'win32') return signalProcessTree(child, 'SIGKILL');
  signalProcessTree(child, 'SIGTERM');
  const timer = setTimeout(() => {
    signalProcessTree(child, 'SIGKILL');
  }, 100);
  timer.unref();
  return timer;
}

function runPluginProcess(invocation, executable, responseLimit, signal, cwd, finalizing = false) {
  return new Promise((resolveResponse, rejectResponse) => {
    if (signal.aborted) { rejectResponse(new Error('cancelled')); return; }
    const child = spawn(executable[0], executable.slice(1), { stdio: ['pipe', 'pipe', 'pipe'], detached: true, shell: false, cwd: cwd ?? process.cwd() });
    track(child, undefined, finalizing);
    const output = [];
    let outputBytes = 0;
    let overflow = false;
    let forceKillTimer;
    let killCompletion = Promise.resolve();
    const stopChild = () => {
      const stopping = terminatePluginChild(child);
      if (stopping && typeof stopping.then === 'function') killCompletion = stopping;
      else forceKillTimer ??= stopping;
    };
    child.stdout.on('data', (chunk) => {
      outputBytes += chunk.length;
      if (outputBytes > responseLimit + 1) {
        overflow = true;
        stopChild();
      } else output.push(Buffer.from(chunk));
    });
    // Drain stderr so a noisy plugin cannot block. It is deliberately omitted
    // from both the returned diagnostic and the public result.
    child.stderr.resume();
    const abort = () => stopChild();
    signal.addEventListener('abort', abort, { once: true });
    child.once('error', () => {
      clearTimeout(forceKillTimer);
      signal.removeEventListener('abort', abort);
      rejectResponse(new Error('plugin process failed'));
    });
    child.once('close', async (code, signalName) => {
      clearTimeout(forceKillTimer);
      await killCompletion;
      signal.removeEventListener('abort', abort);
      if (signal.aborted) { rejectResponse(new Error('plugin process cancelled')); return; }
      if (overflow) { rejectResponse(Object.assign(new Error('plugin response too large'), { code: 'PLUGIN_LIMIT' })); return; }
      if (code !== 0 || signalName !== null) { rejectResponse(new Error('plugin process failed')); return; }
      const bytes = Buffer.concat(output);
      let text;
      try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
      catch { rejectResponse(Object.assign(new Error('invalid plugin response encoding'), { code: 'PLUGIN_PROTOCOL' })); return; }
      if (!text.endsWith('\n') || text.slice(0, -1).includes('\n') || text.endsWith('\n\n')) { rejectResponse(Object.assign(new Error('invalid plugin response record'), { code: 'PLUGIN_PROTOCOL' })); return; }
      try { resolveResponse(JSON.parse(text.slice(0, -1))); }
      catch { rejectResponse(Object.assign(new Error('invalid plugin response record'), { code: 'PLUGIN_PROTOCOL' })); }
    });
    child.stdin.once('error', () => {});
    child.stdin.end(`${JSON.stringify(invocation)}\n`);
  });
}

function apiContext(options, signal) {
  const cwd = resolve(options.cwd ?? process.cwd());
  const grants = { read: false, write: false, run: false, env: false };
  const portableGrants = [];
  const roots = { read: [], write: [], run: [] };
  for (const grant of options.grants ?? []) {
    if (!grant || !['read', 'write', 'run', 'env'].includes(grant.capability)) throw Object.assign(new Error('invalid capability grant'), { code: 'OPT_VALUE_INVALID' });
    const names = [...(grant.names ?? [])];
    if (names.some((name) => typeof name !== 'string')) throw Object.assign(new Error('grant paths must be strings'), { code: 'OPT_VALUE_INVALID' });
    grants[grant.capability] = true;
    portableGrants.push({ capability: grant.capability, names });
    if (grant.capability === 'read' || grant.capability === 'write' || grant.capability === 'run') {
      roots[grant.capability].push(...names.map((name) => resolve(cwd, name)));
    }
  }
  const grantRoots = (capability) => !grants[capability] || portableGrants.some((grant) => grant.capability === capability && grant.names.length === 0)
    ? null : roots[capability];
  return {
    api: true,
    cwd,
    grants,
    portableGrants,
    readRoots: grantRoots('read'),
    writeRoots: grantRoots('write'),
    runRoots: grantRoots('run'),
    environment: [...(options.environment ?? [])],
    shell: [...(options.shell ?? [])],
    plugins: options.plugins ?? [],
    pluginCallbacks: options.pluginCallbacks ?? {},
    toolCache: new Map(),
    events: [],
    signal,
    hostRequest: options.hostRequest,
    json: false,
    human: false,
  };
}

export class Kame {
  constructor(module) {
    this.module = module;
    this.programs = new Set();
    this.operations = new Set();
    this.watches = new Set();
    this.disposed = false;
  }

  static async create(options = {}) {
    const path = options.wasmPath ? resolve(options.wasmPath) : wasmPath;
    return new Kame(await Module.load(path));
  }

  assertLive() {
    if (this.disposed) throw Object.assign(new Error('Kame object has been disposed'), { code: 'DISPOSED' });
  }

  async operation(program, options, callback) {
    this.assertLive();
    if (program?.disposed) throw Object.assign(new Error('Kame program has been disposed'), { code: 'DISPOSED' });
    if (program && (program.watchPending || [...this.watches].some((watch) => watch.program === program && !watch.closed))) throw Object.assign(new Error('close the active watch before starting another operation'), { code: 'BUSY' });
    const controller = new AbortController();
    const external = options?.signal;
    const abort = () => controller.abort();
    external?.addEventListener('abort', abort, { once: true });
    if (external?.aborted) controller.abort();
    const token = { program, controller, promise: null };
    const context = apiContext(options ?? {}, controller.signal);
    token.promise = Promise.resolve().then(() => {
      if (controller.signal.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
      return callback(context);
    });
    this.operations.add(token);
    try {
      return await token.promise;
    } finally {
      external?.removeEventListener('abort', abort);
      this.operations.delete(token);
    }
  }

  async parse(language, source, options = {}) {
    return this.operation(null, options, async (context) => {
      const module = await Module.load(this.module.path);
      if (context.signal.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
      const bytes = await module.parse(language, options.name ?? 'input.km', source);
      return JSON.parse(module.decode0(bytes));
    });
  }

  async format(language, source, options = {}) {
    return this.operation(null, options, async (context) => {
      const module = await Module.load(this.module.path);
      if (context.signal.aborted) throw Object.assign(new Error('invocation cancelled'), { code: 'EXEC_CANCELLED' });
      const bytes = await module.format(language, options.name ?? 'input.km', source, options.indentStyle ?? 'tab', options.indentWidth ?? 4);
      return module.decode0(bytes);
    });
  }

  async compile(source, options = {}) {
    const name = options.name ?? 'input.km';
    const language = options.language ?? 'script';
    return this.operation(null, options, async () => {
      const module = await Module.load(this.module.path);
      const bytes = await module.parse(language, name, source);
      const ast = JSON.parse(module.decode0(bytes));
      if (['script', 'km', 'kmk'].includes(language)) {
        const instance = module.exports.kame_wasm_instance_create();
        if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
        try {
          module.compileBuild(instance, source, name);
          if (module.exports.kame_wasm_prepare(instance) !== 0) throw module.compileFailure(instance, 'PARSE_ERR');
        } finally {
          module.exports.kame_wasm_instance_free(instance);
        }
      }
      const program = new KameProgram(this, module, source, name, language, ast);
      this.programs.add(program);
      return program;
    });
  }

  async dispose() {
    if (!this.disposePromise) this.disposePromise = this.disposeOwned();
    return this.disposePromise;
  }

  async disposeOwned() {
    this.disposed = true;
    for (const operation of this.operations) operation.controller.abort();
    await Promise.allSettled([...this.watches].map((watch) => watch.close()));
    await Promise.allSettled([...this.operations].map((operation) => operation.promise));
    for (const program of this.programs) {
      program.disposed = true;
      program.source = '';
      program._ast = null;
      program.module = null;
    }
    this.programs.clear();
  }
}

export class KameProgram {
  constructor(owner, module, source, name, language, ast) {
    this.owner = owner;
    this.module = module;
    this.source = `${source}`;
    this.name = `${name}`;
    this.language = language;
    this._ast = structuredClone(ast);
    this.disposed = false;
    this.tail = Promise.resolve();
  }

  async serialized(callback) {
    const previous = this.tail;
    let release;
    this.tail = new Promise((resolveSerial) => { release = resolveSerial; });
    await previous;
    try { return await callback(); }
    finally { release(); }
  }

  get ast() {
    this.owner.assertLive();
    if (this.disposed) throw Object.assign(new Error('Kame program has been disposed'), { code: 'DISPOSED' });
    return structuredClone(this._ast);
  }

  async evaluate(expression, options = {}) {
    return this.owner.operation(this, options, async (context) => this.serialized(async () => {
      // Host allocations are monotonic: keep one module per operation, never
      // reset an allocator that an active watch or another owner can still use.
      const module = await Module.load(this.module.path);
      this.module = module;
      const result = await module.evaluate(this.source, expression, context);
      return module.decode0(result);
    }));
  }

  async build(targets, options = {}) {
    const requested = Array.isArray(targets) ? [...targets] : [targets];
    if (requested.some((target) => typeof target !== 'string')) throw Object.assign(new Error('build targets must be strings'), { code: 'OPT_VALUE_INVALID' });
    return this.owner.operation(this, options, async (base) => this.serialized(async () => {
      const module = await Module.load(this.module.path);
      this.module = module;
      const results = await module.materializeMany(this.source, requested, base, this.name);
      return { results: results.map((result, index) => ({ target: requested[index], kind: result.kind, value: module.decode0(result.bytes) })), events: structuredClone(base.events) };
    }));
  }

  async watch(targets, options = {}) {
    this.owner.assertLive();
    if (this.disposed) throw Object.assign(new Error('Kame program has been disposed'), { code: 'DISPOSED' });
    if (this.watchPending || [...this.owner.watches].some((watch) => watch.program === this && !watch.closed)) throw Object.assign(new Error('a watch is already active for this program'), { code: 'BUSY' });
    const names = Array.isArray(targets) ? [...targets] : [targets];
    if (names.some((target) => typeof target !== 'string')) throw Object.assign(new Error('watch targets must be strings'), { code: 'OPT_VALUE_INVALID' });
    const controller = new AbortController();
    const external = options.signal;
    const abort = () => controller.abort();
    external?.addEventListener('abort', abort, { once: true });
    if (external?.aborted) controller.abort();
    const context = apiContext(options, controller.signal);
    this.watchPending = true;
    const token = { program: this, controller, promise: null };
    token.promise = this.serialized(async () => {
      if (controller.signal.aborted || this.disposed || this.owner.disposed) throw Object.assign(new Error('watch creation cancelled'), { code: 'EXEC_CANCELLED' });
      const module = await Module.load(this.module.path);
      this.module = module;
      const watch = await module.beginWatch(this.source, names, this.name, context);
      if (controller.signal.aborted || this.disposed || this.owner.disposed) {
        await module.disposeWatch(watch);
        throw Object.assign(new Error('watch creation cancelled'), { code: 'EXEC_CANCELLED' });
      }
      const apiWatch = new KameWatch(this, watch, controller, external, abort);
      this.owner.watches.add(apiWatch);
      return apiWatch;
    });
    this.owner.operations.add(token);
    try {
      return await token.promise;
    } catch (error) {
      external?.removeEventListener('abort', abort);
      throw error;
    } finally {
      this.watchPending = false;
      this.owner.operations.delete(token);
    }
  }

  async dispose() {
    if (!this.disposePromise) this.disposePromise = this.disposeOwned();
    return this.disposePromise;
  }

  async disposeOwned() {
    this.disposed = true;
    await Promise.allSettled([...this.owner.watches].filter((watch) => watch.program === this).map((watch) => watch.close()));
    const operations = [...this.owner.operations].filter((operation) => operation.program === this);
    for (const operation of operations) operation.controller.abort();
    await Promise.allSettled(operations.map((operation) => operation.promise));
    this.owner.programs.delete(this);
    this.module = null;
  }
}

export class KameWatch {
  constructor(program, watch, controller, externalSignal, abort) {
    this.program = program;
    this.module = program.module;
    this.watch = watch;
    this.controller = controller;
    this.externalSignal = externalSignal;
    this.abort = abort;
    this.closed = false;
    this.started = false;
  }

  assertLive() {
    if (this.closed || this.program.disposed || this.program.owner.disposed) throw Object.assign(new Error('Kame watch has been disposed'), { code: 'DISPOSED' });
  }

  snapshot() {
    this.assertLive();
    return structuredClone(this.module.watchSnapshot(this.watch));
  }

  invalidate(resources) {
    this.assertLive();
    this.module.invalidateWatch(this.watch, structuredClone(resources));
  }

  async next() {
    this.assertLive();
    const module = this.module;
    while (!this.closed && !this.controller.signal.aborted) {
      const state = module.drainWatch(this.watch);
      if (state === 1) module.serviceWatchRequest(this.watch);
      if (this.watch.hostError) throw this.watch.hostError;
      const events = this.watch.context.events.splice(0);
      const snapshot = this.snapshot();
      if (!this.started || events.length !== 0) {
        this.started = true;
        return { done: false, value: { snapshot, events: structuredClone(events) } };
      }
      if (this.watch.pending.size) await Promise.race([...this.watch.pending, new Promise((resolveWait) => setTimeout(resolveWait, 10))]);
      else await new Promise((resolveWait) => setTimeout(resolveWait, 10));
    }
    return { done: true, value: undefined };
  }

  [Symbol.asyncIterator]() { return this; }

  async close() {
    if (!this.closePromise) this.closePromise = this.closeOwned();
    return this.closePromise;
  }

  async closeOwned() {
    this.closed = true;
    this.controller.abort();
    await this.module.disposeWatch(this.watch);
    this.externalSignal?.removeEventListener('abort', this.abort);
    this.program.owner.watches.delete(this);
    this.module = null;
    this.watch = null;
  }
}

// runProcessDirect services a process request without an event stream; used by
// expression evaluation, which has no target program to emit events for.
function runProcessDirect(script, context) {
  if (context.signal?.aborted) return Promise.resolve({ ok: false, code: 'EXEC_CANCELLED', message: 'invocation cancelled' });
  return new Promise((resolveRun) => {
    const shell = context.shell.length !== 0 ? context.shell : defaultShell();
    const childEnv = context.environmentExact ? {} : { ...process.env };
    for (const entry of context.environment) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const child = spawn(shell[0], [...shell.slice(1), script], { stdio: ['ignore', 'pipe', 'pipe'], env: childEnv, cwd: context.cwd ?? process.cwd(), detached: true });
    track(child, undefined, context.finalizing);
    const out = [];
    let failure = null;
    let killCompletion = Promise.resolve();
    const cancelled = () => { killCompletion = Promise.resolve(signalProcessTree(child, 'SIGKILL')); };
    context.signal?.addEventListener('abort', cancelled, { once: true });
    if (context.signal?.aborted) cancelled();
    child.stdout.on('data', (chunk) => out.push(chunk));
    child.stderr.on('data', () => {});
    child.once('error', (error) => { failure = error; });
    child.once('close', async (status) => {
      await killCompletion;
      context.signal?.removeEventListener('abort', cancelled);
      if (context.signal?.aborted) resolveRun({ ok: false, code: 'EXEC_CANCELLED', message: 'invocation cancelled' });
      else if (failure !== null) resolveRun({ ok: false, code: 'HOST_FAIL', message: failure.message });
      else if (status !== 0) resolveRun({ ok: false, code: 'RECIPE_FAIL', message: `process exited with status ${status}` });
      else resolveRun({ ok: true, text: Buffer.concat(out).toString('utf8') });
    });
  });
}

function defaultShell() {
  return process.platform === 'win32'
    ? [env.ComSpec || 'cmd.exe', '/d', '/s', '/c']
    : ['/bin/sh', '-c'];
}

// Structured requests never cross a shell-text boundary. Captured stdout is
// bounded and private; stderr is forwarded while the child is still running.
function processDeadline(milliseconds, callback) {
  const deadline = performance.now() + milliseconds;
  let timer;
  const arm = () => {
    const remaining = deadline - performance.now();
    if (remaining <= 0) callback();
    else timer = setTimeout(arm, Math.min(remaining, 2147483647));
  };
  arm();
  return () => clearTimeout(timer);
}

// Validate the wire graph before opening files or launching any stage. The
// portable evaluator owns language semantics; this is the host trust boundary.
function prepareByteTopology(stages, context) {
  const invalid = (code = 'KASH_MUX_ARITY') => { throw Object.assign(new Error('invalid process graph topology'), { code }); };
  if (context.kinds === undefined) { if ([32, 33, 34, 35, 36, 37, 38].includes(context.wireKind) || context.failurePolicy !== undefined) invalid(); return null; }
  if (context.wireKind === 34 && !Array.isArray(context.buffers)) invalid();
  if (context.wireKind === 35 && (!Array.isArray(context.overflows) || !Array.isArray(context.bufferItems))) invalid();
  if ([36, 37, 38].includes(context.wireKind) && [context.orders, context.buffers, context.overflows, context.bufferItems].some((policy) => !Array.isArray(policy))) invalid();
  const failurePolicy = context.failurePolicy === undefined ? 0 : context.failurePolicy;
  if (!Number.isSafeInteger(failurePolicy) || failurePolicy < 0 || failurePolicy > 1 || (context.wireKind === 37 && context.failurePolicy !== 1)) invalid();
  if (context.wireKind === 38 && context.failurePolicy === undefined) invalid();
  if (process.platform === 'win32') throw Object.assign(new Error('streaming process graphs are not supported by this Windows host'), { code: Array.isArray(context.edges) && context.edges.some((edge) => Array.isArray(edge) && edge[2] > 0) ? 'KASH_STREAM_OPERAND_UNSUPPORTED' : 'HOST_FAIL' });
  const { kinds, edges, setup } = context;
  if (!Array.isArray(stages) || !stages.length || !Array.isArray(kinds) || kinds.length !== stages.length || !Array.isArray(edges) || !Array.isArray(setup) || setup.length !== stages.length || context.input || context.output) invalid();
  if ((context.wireKind === 38 && !kinds.includes('each')) || (context.wireKind !== undefined && context.wireKind !== 38 && kinds.includes('each'))) invalid();
  const inputs = stages.map(() => []), outputs = stages.map(() => []);
  for (let i = 0; i < edges.length; i++) {
    const edge = edges[i];
    if (!Array.isArray(edge) || ![2, 3].includes(edge.length) || edge.slice(0, 2).some((node) => !Number.isInteger(node) || node < 0 || node >= stages.length)) invalid();
    const argument = edge[2] ?? 0;
    if (!Number.isSafeInteger(argument) || argument < 0 || (argument && (kinds[edge[1]] !== '' || !Array.isArray(stages[edge[1]]) || argument >= stages[edge[1]].length))) invalid();
    if (argument && inputs[edge[1]].some((previous) => edges[previous][2] === argument)) invalid('KASH_DOUBLE_CONSUME');
    outputs[edge[0]].push(i); inputs[edge[1]].push(i);
  }
  for (let i = 0; i < stages.length; i++) {
    if (!Array.isArray(stages[i]) || !['', 'merge', 'broadcast', 'balance', 'frame', 'encode', 'filter', 'copy', 'each'].includes(kinds[i])) invalid();
    if (!kinds[i] || kinds[i] === 'each') {
      const stdin = inputs[i].filter((edge) => !(edges[edge][2] ?? 0));
      if (!stages[i].length || stdin.length > 1 || outputs[i].length > 1) invalid();
      if (kinds[i] === 'each' && stdin.length !== 1) invalid();
      if ((stdin.length && setup[i].input) || (outputs[i].length && setup[i].output)) invalid('KASH_DOUBLE_CONSUME');
    } else {
      if (stages[i].length || setup[i].input || setup[i].output) invalid();
      if (kinds[i] === 'merge') { if (inputs[i].length < 2 || outputs[i].length !== 1) invalid(); }
      else if (kinds[i] === 'broadcast' || kinds[i] === 'balance') { if (inputs[i].length !== 1 || outputs[i].length < 2) invalid(); }
      else if (kinds[i] === 'filter') { if (inputs[i].length !== 1 || outputs[i].length !== 2) invalid(); }
      else if (inputs[i].length !== 1 || outputs[i].length > 1) invalid();
    }
  }
  const remaining = inputs.map((ports) => ports.length), ready = [];
  remaining.forEach((count, node) => { if (!count) ready.push(node); });
  for (let cursor = 0; cursor < ready.length; cursor++) for (const edge of outputs[ready[cursor]]) if (--remaining[edges[edge][1]] === 0) ready.push(edges[edge][1]);
  if (ready.length !== stages.length) invalid('KASH_GRAPH_CYCLE');
  const framed = context.framed ?? stages.map(() => false);
  if (!Array.isArray(framed) || framed.length !== stages.length || framed.some((value) => typeof value !== 'boolean')) invalid('KASH_FRAME_MISMATCH');
  for (const node of ready) {
    const inputKinds = inputs[node].map((edge) => framed[edges[edge][0]]);
    if (inputKinds.some((kind) => kind !== inputKinds[0])) invalid('KASH_FRAME_MISMATCH');
    const kind = kinds[node], input = inputKinds[0] ?? false;
    if ((kind === 'balance' || kind === 'each') && !input) invalid('KASH_FRAME_REQUIRED');
    if (framed[node] && !outputs[node].length) invalid('KASH_FRAME_MISMATCH');
    const expected = kind === 'frame' || kind === 'filter' ? true : kind === 'encode' || kind === '' || kind === 'each' ? false : input;
    if (framed[node] !== expected || (kind === 'frame' && input) || (['filter', 'encode'].includes(kind) && !input) || (!kind && input)) invalid('KASH_FRAME_MISMATCH');
  }
  const buffers = context.buffers === undefined ? stages.map(() => 0) : context.buffers;
  if (!Array.isArray(buffers) || buffers.length !== stages.length || buffers.some((budget, node) => !Number.isSafeInteger(budget) || budget < 0 || budget > 65536 || (budget && !['merge', 'broadcast', 'balance'].includes(kinds[node])))) invalid();
  const overflows = context.overflows === undefined ? stages.map(() => 0) : context.overflows;
  const bufferItems = context.bufferItems === undefined ? stages.map(() => 0) : context.bufferItems;
  if (!Array.isArray(overflows) || overflows.length !== stages.length || !Array.isArray(bufferItems) || bufferItems.length !== stages.length) invalid();
  for (let node = 0; node < stages.length; node++) {
    const policy = overflows[node], items = bufferItems[node];
    if (!Number.isSafeInteger(policy) || policy < 0 || policy > 3 || !Number.isSafeInteger(items) || items < 0 || items > 1024 || (!policy && items) || (policy && kinds[node] !== 'broadcast') || (policy === 3 && items > 1)) invalid();
    if (policy && !framed[node]) invalid('KASH_FRAME_REQUIRED');
  }
  const orders = context.orders === undefined ? stages.map(() => 0) : context.orders;
  if (!Array.isArray(orders) || orders.length !== stages.length || orders.some((order, node) => !Number.isSafeInteger(order) || order < 0 || order > 1 || (order && kinds[node] !== 'merge'))) invalid();
  if (orders.some((order, node) => order && !framed[node])) invalid('KASH_FRAME_REQUIRED');
  const edgeMetrics = edges.map(([from, to]) => [kinds[from] ? overflows[from] ? 3 : 1 : kinds[to] ? 2 : 0, 0, framed[from] ? 0 : -1, framed[from] ? 0 : -1, 0, 0, 0]);
  return { kinds, edges, inputs, outputs, framed, buffers, overflows, bufferItems, orders, edgeMetrics, failurePolicy };
}

function metricAdd(metric, field, amount) {
  if (!metric) return;
  if (amount > Number.MAX_SAFE_INTEGER - metric[field]) { metric[field] = Number.MAX_SAFE_INTEGER; metric[6] = 1; }
  else metric[field] += amount;
}

function metricWrite(metric, length) {
  if (!metric || ![1, 3].includes(metric[0])) return;
  metricAdd(metric, 1, length - (metric[2] >= 0 ? 4 : 0));
  if (metric[2] >= 0) metricAdd(metric, 2, 1);
}

// Paused merges, broadcasts, and balancing share one bounded pump. Each turn
// retains one chunk/record until its assigned destinations flush; incomplete
// records are bounded per input. Defaults are 16 KiB bytes / 64 KiB records.
function startByteMux(readers, writers, fail, done, kind = '', framed = false, item = null, budget = 0, overflow = 0, itemLimit = 0, metrics = null, setState = () => {}, order = 0, worker = null) {
  let cursor = 0, destination = 0, scheduled = false, pending = false, closing = false, disposed = false;
  const recordLimit = budget || 64 * 1024, byteLimit = budget || 16 * 1024;
  const queueLimit = itemLimit || (overflow === 3 ? 1 : 64);
  const queues = overflow ? writers.map(() => ({ pending: [], bytes: 0, writing: false, ending: false })) : [];
  const writerIndices = writers.map((_, index) => index);
  const states = readers.map(() => ({ data: framed || kind === 'frame' || kind === 'encode' || kind === 'each' ? Buffer.alloc(recordLimit + 4) : null, length: 0, need: 4, chunk: null, offset: 0 }));
  const operatorError = (status) => { fail(status); return null; };
  const readChunk = (reader, size, index) => {
    const chunk = reader.read(size), metric = metrics?.inputs[index];
    if (chunk !== null && metric?.[0] === 2) metricAdd(metric, 1, chunk.length);
    return chunk;
  };
  const readItem = (reader, state, index) => {
    if (kind === 'frame') {
      for (;;) {
        if (state.chunk === null) {
          state.chunk = readChunk(reader, Math.min(16 * 1024, reader.readableLength || 16 * 1024), index);
          state.offset = 0;
          if (state.chunk === null) {
            if (!reader.readableEnded || !state.length) return null;
            break;
          }
        }
        const newline = state.chunk.indexOf(10, state.offset);
        const end = newline < 0 ? state.chunk.length : newline + 1;
        const size = end - state.offset;
        if (state.length + size > recordLimit) return operatorError(121);
        state.chunk.copy(state.data, 4 + state.length, state.offset, end);
        state.length += size; state.offset = end;
        if (end === state.chunk.length) state.chunk = null;
        if (newline >= 0) break;
      }
      const valid = item(-1, state.data.subarray(4, 4 + state.length));
      if (valid < 0) return operatorError(-valid);
      state.data.writeUInt32LE(state.length, 0);
      const record = state.data.subarray(0, 4 + state.length);
      state.length = 0;
      return record;
    }
    if (!framed && kind !== 'encode' && kind !== 'each') return readChunk(reader, Math.min(byteLimit, reader.readableLength || byteLimit), index);
    for (;;) {
      const chunk = readChunk(reader, Math.min(state.need - state.length, reader.readableLength || state.need - state.length), index);
      if (chunk === null) {
        if (reader.readableEnded && state.length) return operatorError(124);
        return null;
      }
      chunk.copy(state.data, state.length); state.length += chunk.length;
      if (state.length < state.need) return null;
      if (state.need === 4) {
        const size = state.data.readUInt32LE(0);
        if (!size || size > 64 * 1024) return operatorError(121);
        if (size > recordLimit) return operatorError(126);
        state.need = 4 + size;
      } else {
        const record = state.data.subarray(kind === 'encode' || kind === 'each' ? 4 : 0, state.need);
        state.length = 0; state.need = 4;
        return record;
      }
    }
  };
  let outputs = writers.length;
  const dispose = () => {
    disposed = true;
    for (const queue of queues) { queue.pending.length = 0; queue.bytes = 0; }
    for (const stream of [...readers, ...writers]) stream.destroy();
  };
  const error = (cause) => { if (!disposed) fail(['EPIPE', 'ECONNRESET'].includes(cause.code) ? 'RECIPE_FAIL' : 'HOST_FAIL', `process operator I/O failed: ${cause.message}`); };
  const flushQueue = (index) => {
    const queue = queues[index], writer = writers[index];
    if (disposed || queue.writing || queue.ending) return;
    if (!queue.pending.length) {
      if (closing) { queue.ending = true; writer.end(() => writer.destroy()); }
      return;
    }
    const record = queue.pending.shift();
    queue.bytes -= record.length - 4;
    queue.writing = true;
    setState(3);
    writer.write(record, (cause) => {
      if (cause) error(cause);
      else metricWrite(metrics?.outputs[index], record.length);
      queue.writing = false;
      flushQueue(index);
      if (!queues.some((queue) => queue.writing || queue.pending.length)) setState(closing ? 1 : 2);
    });
  };
  const queueRecord = (record) => {
    const payload = record.length - 4;
    let retained;
    for (let index = 0; index < queues.length; index++) {
      const queue = queues[index];
      const metric = metrics?.outputs[index];
      if (overflow === 1 && (queue.pending.length === queueLimit || queue.bytes + payload > recordLimit)) { metricAdd(metric, 3, 1); continue; }
      // ponytail: shifts are bounded by 1024 records; use a ring if profiled hot.
      while (queue.pending.length && (overflow === 3 || queue.pending.length === queueLimit || queue.bytes + payload > recordLimit)) { queue.bytes -= queue.pending.shift().length - 4; metricAdd(metric, 3, 1); }
      retained ??= Buffer.from(record); // Never retain the mutable assembler view.
      queue.pending.push(retained); queue.bytes += payload;
      if (metric) { metric[4] = Math.max(metric[4], queue.bytes); metric[5] = Math.max(metric[5], queue.pending.length); }
      flushQueue(index);
    }
  };
  const schedule = () => {
    if (scheduled || pending || closing || disposed) return;
    scheduled = true;
    setImmediate(pump);
  };
  const pump = () => {
    scheduled = false;
    if (pending || closing || disposed) return;
    setState(1);
    for (let offset = 0; offset < readers.length; offset++) {
      const next = (cursor + offset) % readers.length, reader = readers[next];
      const chunk = readItem(reader, states[next], next);
      if (disposed) return;
      if (chunk === null) {
        if (order && !reader.readableEnded) { setState(2); return; }
        continue;
      }
      cursor = (next + 1) % readers.length;
      if (overflow) { queueRecord(chunk); schedule(); return; }
      pending = true;
      if (kind === 'each') {
        setState(1);
        worker(chunk, writers[0], () => { pending = false; schedule(); });
        return;
      }
      setState(3);
      let destinations = writerIndices;
      if (kind === 'filter') {
        const selected = item(0, chunk.subarray(4));
        if (selected < 0) { operatorError(-selected); return; }
        const index = selected ? 0 : 1;
        destinations = [index];
      } else if (kind === 'balance') {
        destinations = [destination];
        destination = (destination + 1) % writers.length;
      }
      let outstanding = destinations.length;
      for (const index of destinations) writers[index].write(chunk, (cause) => {
        if (cause) error(cause);
        else metricWrite(metrics?.outputs[index], chunk.length);
        if (--outstanding === 0) { pending = false; schedule(); }
      });
      return;
    }
    setState(overflow && queues.some((queue) => queue.writing || queue.pending.length) ? 3 : 2);
    if (readers.every((reader) => reader.readableEnded)) {
      closing = true;
      if (overflow) queues.forEach((_, index) => flushQueue(index));
      else for (const writer of writers) writer.end(() => writer.destroy());
    }
  };
  for (const reader of readers) {
    reader.on('error', error);
    reader.on('readable', schedule);
    reader.on('end', schedule);
    reader.on('close', () => { if (!disposed && !reader.readableEnded) error(new Error('upstream closed before EOF')); });
  }
  for (const [index, writer] of writers.entries()) {
    writer.on('error', error);
    writer.on('close', () => {
      if (!disposed && (overflow ? !queues[index].ending : !closing)) error(Object.assign(new Error('downstream closed early'), { code: 'EPIPE' }));
      if (--outputs === 0) done();
    });
  }
  schedule();
  return dispose;
}

function runArgvCapture(stages, context) {
  if (context.signal?.aborted) return Promise.resolve({ ok: false, code: 'EXEC_CANCELLED', message: 'invocation cancelled' });
  let topology;
  try { topology = prepareByteTopology(stages, context); }
  catch (error) { return Promise.resolve({ ok: false, code: error.code ?? 'KASH_MUX_ARITY', message: error.message }); }
  if (!Array.isArray(stages) || stages.length === 0 || stages.some((args, i) => !Array.isArray(args) || ((!topology?.kinds[i]) && (args.length === 0 || !args[0])) || args.some((arg) => typeof arg !== 'string' || arg.includes('\0')))) {
    return Promise.resolve({ ok: false, code: 'EXPR_INVALID', message: 'invalid process argv' });
  }
  const setups = context.setup ?? [];
  if (!Array.isArray(setups) || (setups.length !== 0 && setups.length !== stages.length) || setups.some((setup) => typeof setup.cwd !== 'string' || setup.cwd.includes('\0') || !Number.isSafeInteger(setup.timeoutMS) || setup.timeoutMS < 0 || !Array.isArray(setup.environment) || setup.environment.some((entry) => typeof entry !== 'string' || entry.includes('\0') || !/^[A-Za-z_][A-Za-z0-9_]*=/.test(entry)))) {
    return Promise.resolve({ ok: false, code: 'EXPR_INVALID', message: 'invalid stage setup' });
  }
  return new Promise((resolveRun, rejectRun) => {
    const childEnv = context.environmentExact ? {} : { ...process.env };
    for (const entry of context.environment ?? []) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const configurations = stages.map((_, i) => {
      const setup = setups[i];
      const env = { ...childEnv };
      for (const entry of setup?.environment ?? []) { const at = entry.indexOf('='); env[entry.slice(0, at)] = entry.slice(at + 1); }
      return { env, cwd: setup?.cwd || context.cwd || process.cwd(), timeoutMS: setup?.timeoutMS ?? 0 };
    });
    let executables;
    try {
      for (const configuration of configurations) if (!statSync(configuration.cwd).isDirectory()) throw new Error('invalid stage cwd');
      executables = stages.map((args, i) => topology?.kinds[i] && topology.kinds[i] !== 'each' ? null : argvExecutable(args[0], configurations[i].env, configurations[i].cwd));
    }
    catch { resolveRun({ ok: false, code: 'HOST_FAIL', message: 'cannot start process' }); return; }
    const argumentEdges = topology ? topology.edges.map((edge, index) => edge[2] > 0 ? index : -1).filter((edge) => edge >= 0) : [];
    let pipes;
    const terminalOperators = topology ? topology.kinds.map((kind, node) => kind && !topology.outputs[node].length && !setups[node].output ? node : -1).filter((node) => node >= 0) : [];
    try { pipes = pipelinePipes(topology ? topology.edges.length + terminalOperators.length : stages.length - 1); }
    catch { resolveRun({ ok: false, code: argumentEdges.length ? 'KASH_STREAM_OPERAND_UNSUPPORTED' : 'HOST_FAIL', message: 'cannot create pipeline pipes (POSIX mkfifo required)' }); return; }
    let operandFiles, relayExecutable, relayShell;
    try {
      if (argumentEdges.length) {
        relayExecutable = argvExecutable(existsSync('/usr/bin/dd') ? '/usr/bin/dd' : '/bin/dd', process.env);
        relayShell = argvExecutable('/bin/sh', process.env);
        operandFiles = streamOperandFifos(argumentEdges.length);
      }
    } catch {
      for (const pipe of pipes) { closeSync(pipe.read); closeSync(pipe.write); }
      resolveRun({ ok: false, code: 'KASH_STREAM_OPERAND_UNSUPPORTED', message: 'live stream operands require POSIX FIFO rendezvous support (mkfifo, sh, and dd)' });
      return;
    }
    let input, output;
    const stageInputs = [], stageOutputs = [];
    try {
      // Input opens first: missing sources must not truncate destinations.
      if (context.input) input = openSync(context.input, 'r');
      if (topology) for (let i = 0; i < stages.length; i++) if (setups[i].input) stageInputs[i] = openSync(setups[i].input, 'r');
      if (context.output) output = openSync(context.output, context.append ? 'a' : 'w');
      if (topology) {
        for (let i = 0; i < stages.length; i++) if (setups[i].output) stageOutputs[i] = openSync(setups[i].output, fsConstants.O_WRONLY | fsConstants.O_CREAT | (setups[i].append ? fsConstants.O_APPEND : 0), 0o666);
        const truncate = stageOutputs.map((fd, i) => !setups[i].append && fstatSync(fd).isFile());
        for (let i = 0; i < stageOutputs.length; i++) if (truncate[i]) ftruncateSync(stageOutputs[i], 0);
      }
    } catch {
      if (input !== undefined) closeSync(input);
      if (output !== undefined) closeSync(output);
      for (const fd of [...stageInputs, ...stageOutputs]) if (fd !== undefined) closeSync(fd);
      for (const pipe of pipes) { closeSync(pipe.read); closeSync(pipe.write); }
      operandFiles?.dispose();
      resolveRun({ ok: false, code: 'HOST_FAIL', message: 'cannot open process redirection' });
      return;
    }
    const children = [];
    const operandStates = new Map(argumentEdges.map((edge) => [edge, { ready: false, done: false, anchor: undefined }]));
    const results = [];
    const liveStates = stages.map(() => 1);
    const chunks = [];
    const limit = context.captureLimit > 0 ? context.captureLimit : 1024 * 1024;
    let length = 0;
    let failure = null;
    let graphFailure = null;
    const operators = [];
    const operatorStreams = [];
    let publicationFailure = null;
    let stopCompletion = Promise.resolve();
    let finishing = false;
    const timers = [];
    let remaining = 0;
    let launched = false;
    const stop = (code, message) => {
      if (failure !== null || graphFailure !== null) return;
      failure = { ok: false, code, message };
      stopCompletion = Promise.all(children.map((child) => signalProcessTree(child, 'SIGKILL')));
      for (const dispose of operators) dispose();
      for (const stream of operatorStreams) stream.destroy();
      syncOutputReaders();
    };
    const stopGraph = (result) => {
      if (failure !== null || graphFailure !== null) return;
      graphFailure = result;
      stopCompletion = Promise.all(children.map((child) => signalProcessTree(child, 'SIGKILL')));
      for (const dispose of operators) dispose();
      for (const stream of operatorStreams) stream.destroy();
      syncOutputReaders();
    };
    const publish = (callback, chunk) => {
      if (failure !== null) return;
      try { callback(chunk); }
      catch (error) { publicationFailure = error; stop(error.code ?? 'HOST_FAIL', error.message); }
    };
    const receiveOutput = (chunk) => {
      if (context.stream) { publish(context.onStdout, chunk); return; }
      if (context.discardStdout) return;
      length += chunk.length;
      if (length > limit) stop('CAPTURE_LIMIT', 'command substitution exceeded capture limit');
      else if (failure === null) chunks.push(chunk);
    };
    const cancelled = () => stop('EXEC_CANCELLED', 'invocation cancelled');
    context.signal?.addEventListener('abort', cancelled, { once: true });
    if (context.timeoutMS > 0) timers.push(processDeadline(context.timeoutMS, () => stop('RECIPE_TIMEOUT', 'process timed out')));
    const finish = () => {
      if (!launched || remaining !== 0 || finishing) return;
      finishing = true;
      void stopCompletion.then(() => {
      finishing = false;
      if (remaining !== 0) return;
      for (const cancel of timers) cancel();
      if (topology && context.onSnapshot) publish(context.onSnapshot, { states: liveStates, edgeMetrics: topology.edgeMetrics });
      context.signal?.removeEventListener('abort', cancelled);
      try { operandFiles?.dispose(); } catch (error) { failure ??= { ok: false, code: 'HOST_FAIL', message: `cannot release stream operand: ${error.message}` }; }
      if (publicationFailure !== null) { rejectRun(publicationFailure); return; }
      if (failure !== null) { resolveRun(failure); return; }
      let status = 0, signal = 0;
      for (const result of results) {
        if (result.status !== 0 || result.signal !== 0) { status = result.signal ? 128 + result.signal : result.status; signal = result.signal; }
      }
      if (graphFailure !== null) { status = graphFailure.signal ? 128 + graphFailure.signal : graphFailure.status; signal = graphFailure.signal; }
      // Let the portable evaluator classify exit failure before decoding output.
      const terminal = { status, signal, stages: results, stdout: '' };
      if (topology) terminal.edgeMetrics = topology.edgeMetrics;
      if ((!context.acceptExit && (status !== 0 || signal !== 0)) || context.stream) { resolveRun({ ok: true, value: terminal }); return; }
      let output;
      try { output = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(Buffer.concat(chunks)); }
      catch { resolveRun({ ok: false, code: 'CAPTURE_ENCODING', message: 'command substitution output is not valid UTF-8' }); return; }
      terminal.stdout = output;
      resolveRun({ ok: true, value: terminal });
      });
    };
    try {
      for (let i = 0; i < stages.length && failure === null; i++) {
        if (topology?.kinds[i]) continue;
        const args = [...stages[i]];
        const configuration = configurations[i];
        const inputEdge = topology?.inputs[i].find((edge) => !(topology.edges[edge][2] ?? 0)), outputEdge = topology?.outputs[i][0];
        const operands = topology ? topology.inputs[i].filter((edge) => topology.edges[edge][2] > 0) : [];
        for (const edge of operands) args[topology.edges[edge][2]] = operandFiles.names[argumentEdges.indexOf(edge)];
        const childInput = topology ? inputEdge === undefined ? stageInputs[i] ?? 'ignore' : pipes[inputEdge].read : i === 0 ? input ?? 'ignore' : process.platform === 'win32' ? 'pipe' : pipes[i - 1].read;
        const childOutput = topology ? outputEdge === undefined ? stageOutputs[i] ?? 'pipe' : pipes[outputEdge].write : i === stages.length - 1 ? output ?? 'pipe' : process.platform === 'win32' ? 'pipe' : pipes[i].write;
        const child = spawn(executables[i], args.slice(1), { argv0: args[0], shell: false, stdio: [childInput, childOutput, 'pipe'], env: configuration.env, cwd: configuration.cwd, detached: true });
        children.push(child);
        child.once('exit', () => {
          for (const edge of operands) {
            const state = operandStates.get(edge);
            if (state.ready || state.done) continue;
            // A receiver may never open its operand. Wake a blocked transport
            // open, retaining this anchor only until its explicit ready byte.
            // Once closed, normal closed-reader/SIGPIPE propagation applies.
            try { state.anchor = openSync(operandFiles.names[argumentEdges.indexOf(edge)], fsConstants.O_RDWR | fsConstants.O_NONBLOCK); }
            catch (error) { stop('HOST_FAIL', `cannot close stream operand: ${error.message}`); }
          }
        });
        if (process.platform === 'win32' && i > 0) {
          const upstream = children[i - 1].stdout;
          const closeInput = () => {
            upstream.unpipe(child.stdin);
            upstream.destroy();
          };
          const inputError = (error) => {
            // Closing stdin is ordinary even while the consumer remains alive.
            // Close the upstream read end so the producer observes broken output.
            if (['EPIPE', 'ECONNRESET'].includes(error.code)) {
              closeInput();
              child.stdin.destroy();
              return;
            }
            stop('HOST_FAIL', `pipeline input failed: ${error.message}`);
          };
          child.stdin.on('error', inputError);
          child.stdin.once('close', () => { closeInput(); child.stdin.removeListener('error', inputError); });
          upstream.pipe(child.stdin);
        }
        remaining++;
        if (children.length === 1 && context.onStarted) publish(() => { context.processGroup = context.onStarted(); });
        track(child, context.processGroup, context.finalizing);
        const cancel = configuration.timeoutMS > 0 ? processDeadline(configuration.timeoutMS, () => stop('RECIPE_TIMEOUT', 'stage timed out')) : () => {};
        timers.push(cancel);
        child.once('exit', cancel);
        publishReader(child.stderr, (chunk) => { publish((bytes) => { if (context.onStderr) context.onStderr(bytes); else stderr.write(bytes); }, chunk); }, () => failure !== null || context.signal?.aborted);
        child.once('error', () => stop('HOST_FAIL', 'cannot start process'));
        child.once('close', (status, signalName) => {
          results[i] = { status: status ?? 0, signal: osConstants.signals[signalName] ?? 0, outcome: 0 };
          liveStates[i] = status || signalName ? 5 : 4;
          if (topology && failure === null && graphFailure === null && (results[i].status || results[i].signal) && (!topology.failurePolicy || results[i].signal)) {
            stopGraph(results[i]);
          }
          remaining--;
          finish();
        });
        if (child.stdout !== null) {
        const receive = (chunk) => {
          if (context.stream) { publish(context.onStdout, chunk); return; }
          if (context.discardStdout) return;
            length += chunk.length;
            if (length > limit) stop('CAPTURE_LIMIT', 'command substitution exceeded capture limit');
            else if (failure === null) chunks.push(chunk);
          };
          if (context.stream) publishReader(child.stdout, receive, () => failure !== null || context.signal?.aborted);
          else child.stdout.on('data', receive);
        }
      }
      // Named FIFOs need a real open rendezvous. A fixed POSIX helper opens the
      // output, acknowledges it on fd 4, then becomes dd. No user command or
      // stream data is parsed by this shell: paths are separate quoted argv.
      // The acknowledgement also makes a never-opened operand cancellable
      // without timing guesses or a permanent anchor that suppresses EOF.
      // ponytail: one owned POSIX relay per argument; replace with an anonymous
      // pipe binding if Node gains a supported pipe()/FD-transfer API.
      if (failure === null) for (let slot = 0; slot < argumentEdges.length; slot++) {
        const edge = argumentEdges[slot];
        const state = operandStates.get(edge);
        const relay = spawn(relayShell, ['-c', 'exec 3>"$1"; printf r >&4; exec "$2" bs=16384 >&3', 'kame-stream', operandFiles.names[slot], relayExecutable], { shell: false, stdio: [pipes[edge].read, 'ignore', 'ignore', 'ignore', 'pipe'], env: { PATH: '/usr/bin:/bin', LC_ALL: 'C' }, detached: true });
        children.push(relay); remaining++;
        track(relay, context.processGroup, context.finalizing);
        const readyStream = relay.stdio?.[4];
        readyStream?.once('data', () => {
          state.ready = true;
          if (state.anchor !== undefined) { closeSync(state.anchor); state.anchor = undefined; }
          readyStream.destroy();
        });
        readyStream?.on('error', (error) => stop('HOST_FAIL', `stream operand handshake failed: ${error.message}`));
        relay.once('error', () => stop('HOST_FAIL', 'cannot start stream operand relay'));
        relay.once('close', (status, signalName) => {
          state.done = true;
          if (state.anchor !== undefined) { closeSync(state.anchor); state.anchor = undefined; }
          const result = { status: status ?? 0, signal: osConstants.signals[signalName] ?? 0, outcome: 0 };
          if (result.status || result.signal) stopGraph(result);
          remaining--; finish();
        });
      }
      if (topology && failure === null) for (let i = 0; i < stages.length; i++) if (topology.kinds[i]) {
        let workerResult = { status: 0, signal: 0, outcome: 0 };
        const worker = (record, writer, next) => {
          if (failure !== null || graphFailure !== null) return;
          const args = stages[i], configuration = configurations[i];
          const child = spawn(executables[i], args.slice(1), { argv0: args[0], shell: false, stdio: ['pipe', 'pipe', 'pipe'], env: configuration.env, cwd: configuration.cwd, detached: true });
          children.push(child); remaining++;
          track(child, context.processGroup, context.finalizing);
          let writing = false, closed = false;
          const advance = () => { if (closed && !writing) next(); };
          child.stdout.on('data', (chunk) => {
            child.stdout.pause(); writing = true; liveStates[i] = 3;
            writer.write(chunk, (cause) => {
              writing = false;
              if (cause) stop('HOST_FAIL', 'per-record worker output failed');
              else { metricWrite(topology.edgeMetrics[topology.outputs[i][0]], chunk.length); liveStates[i] = 1; child.stdout.resume(); advance(); }
            });
          });
          publishReader(child.stderr, (chunk) => publish((bytes) => { if (context.onStderr) context.onStderr(bytes); else stderr.write(bytes); }, chunk), () => failure !== null || context.signal?.aborted);
          child.stdin.on('error', () => stop('HOST_FAIL', 'per-record worker input failed'));
          child.stdout.on('error', () => stop('HOST_FAIL', 'per-record worker output failed'));
          child.once('error', () => stop('HOST_FAIL', 'cannot start per-record worker'));
          // Do not accumulate completed ChildProcess handles or detached descendants.
          child.once('exit', () => { void signalProcessTree(child, 'SIGKILL'); });
          child.once('close', (status, signalName) => {
            const result = { status: status ?? 0, signal: osConstants.signals[signalName] ?? 0, outcome: 0 };
            if (result.status || result.signal) { workerResult = result; if (!topology.failurePolicy || result.signal) { results[i] = result; liveStates[i] = 5; stopGraph(result); } }
            children.splice(children.indexOf(child), 1);
            closed = true; remaining--; advance(); finish();
          });
          child.stdin.end(record);
        };
        if (topology.kinds[i] === 'each' && configurations[i].timeoutMS > 0) timers.push(processDeadline(configurations[i].timeoutMS, () => { if (liveStates[i] < 4) stop('RECIPE_TIMEOUT', 'worker stage timed out'); }));
        const failMux = (code, message) => {
          liveStates[i] = 5;
          if (Number.isInteger(code)) {
            results[i] = { status: code, signal: 0, outcome: 0 };
            stopGraph(results[i]);
            return;
          }
          if (code === 'RECIPE_FAIL') {
            results[i] = { status: 141, signal: osConstants.signals.SIGPIPE, outcome: 0 };
            stopGraph(results[i]);
          } else stop(code, message);
        };
        const outputEdges = [...topology.outputs[i]];
        if (!outputEdges.length && !setups[i].output) {
          const edge = topology.edges.length + terminalOperators.indexOf(i);
          outputEdges.push(edge);
          const reader = new Socket({ fd: pipes[edge].read, readable: true, writable: false });
          pipes[edge].read = undefined;
          operatorStreams.push(reader);
          // Join presentation EOF too: the writer may close before this reader
          // has delivered its last bytes to capture or the public output queue.
          remaining++;
          reader.on('error', (error) => failMux('HOST_FAIL', error.message));
          reader.once('close', () => { remaining--; finish(); });
          if (context.stream) publishReader(reader, receiveOutput, () => failure !== null || context.signal?.aborted);
          else reader.on('data', receiveOutput);
        }
        operators.push(startByteMux(topology.inputs[i].map((edge) => {
          const reader = new Socket({ fd: pipes[edge].read, readable: true, writable: false });
          pipes[edge].read = undefined;
          operatorStreams.push(reader);
          reader.on('error', (error) => failMux(['EPIPE', 'ECONNRESET'].includes(error.code) ? 'RECIPE_FAIL' : 'HOST_FAIL', error.message));
          return reader;
        }), setups[i].output ? [(() => {
          const writer = createWriteStream('', { fd: stageOutputs[i], autoClose: true });
          stageOutputs[i] = undefined;
          operatorStreams.push(writer);
          return writer;
        })()] : outputEdges.map((edge) => {
          const writer = new Socket({ fd: pipes[edge].write, readable: false, writable: true });
          pipes[edge].write = undefined;
          operatorStreams.push(writer);
          writer.on('error', (error) => failMux(['EPIPE', 'ECONNRESET'].includes(error.code) ? 'RECIPE_FAIL' : 'HOST_FAIL', error.message));
          return writer;
        }), failMux, () => {
          results[i] ??= workerResult;
          if (liveStates[i] !== 5) liveStates[i] = failure !== null || graphFailure !== null ? 6 : 4;
          if (results[i].status || results[i].signal) liveStates[i] = 5;
          remaining--;
          finish();
        }, topology.kinds[i], topology.framed[i], (node, raw) => context.streamItem(node < 0 ? -1 : i, raw), topology.buffers[i], topology.overflows[i], topology.bufferItems[i], { inputs: topology.inputs[i].map((edge) => topology.edgeMetrics[edge]), outputs: outputEdges.map((edge) => topology.edgeMetrics[edge]) }, (state) => { if (!results[i] && liveStates[i] !== 5) liveStates[i] = state; }, topology.orders[i], worker));
        remaining++;
      }
    } catch { stop('HOST_FAIL', 'cannot start process'); }
    finally {
      for (const fd of [input, output, ...stageInputs, ...stageOutputs]) {
        if (fd !== undefined) { try { closeSync(fd); } catch { stop('HOST_FAIL', 'cannot close redirection'); } }
      }
      for (const pipe of pipes) for (const fd of [pipe.read, pipe.write]) {
        if (fd === undefined) continue;
        try { closeSync(fd); } catch { stop('HOST_FAIL', 'cannot close pipeline pipe'); }
      }
    }
    launched = true;
    if (topology && context.onSnapshot && failure === null) {
      const snapshot = () => publish(context.onSnapshot, { states: [...liveStates], edgeMetrics: topology.edgeMetrics });
      snapshot();
      const timer = setInterval(snapshot, 100);
      timers.push(() => clearInterval(timer));
    }
    finish();
  });
}

// Node's 'pipe' stdio uses socketpairs on Unix; unread bytes on an early close
// can turn SIGPIPE into ECONNRESET/exit 1. Pass real blocking FIFO descriptors to
// the children instead. mkfifo is a POSIX host utility, never a user shell.
function streamOperandFifos(count) {
  const directory = mkdtempSync(join(tmpdir(), 'kame-operands-'));
  const names = Array.from({ length: count }, (_, index) => join(directory, String(index)));
  const dispose = () => {
    for (const name of names) { try { unlinkSync(name); } catch (error) { if (error.code !== 'ENOENT') throw error; } }
    try { rmdirSync(directory); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  };
  try {
    const utility = existsSync('/usr/bin/mkfifo') ? '/usr/bin/mkfifo' : '/bin/mkfifo';
    const result = spawnSync(utility, names, { shell: false, stdio: 'ignore', timeout: 1000, killSignal: 'SIGKILL' });
    if (result.error || result.status !== 0 || names.some((name) => !statSync(name).isFIFO())) throw new Error('FIFO operands unavailable');
    return { names, dispose };
  } catch (error) { dispose(); throw error; }
}

function pipelinePipes(count) {
  if (count === 0) return [];
  if (process.platform === 'win32') return [];
  const directory = mkdtempSync(join(tmpdir(), 'kame-pipes-'));
  const names = Array.from({ length: count }, (_, index) => join(directory, String(index)));
  const pipes = [];
  let failure = null;
  try {
    const utility = existsSync('/usr/bin/mkfifo') ? '/usr/bin/mkfifo' : '/bin/mkfifo';
    const result = spawnSync(utility, names, { shell: false, stdio: 'ignore', timeout: 1000, killSignal: 'SIGKILL' });
    if (result.error || result.status !== 0) throw new Error('mkfifo unavailable');
    for (const name of names) {
      // The temporary RDWR anchor lets both blocking endpoints open without
      // deadlock. It is closed before launch so only actual writers govern EOF.
      const anchor = openSync(name, fsConstants.O_RDWR);
      let read, write;
      try {
        read = openSync(name, fsConstants.O_RDONLY);
        write = openSync(name, fsConstants.O_WRONLY);
        pipes.push({ read, write });
      } catch (error) { if (read !== undefined) closeSync(read); throw error; }
      finally { closeSync(anchor); }
    }
  } catch (error) {
    failure = error;
  } finally {
    // Open descriptors remain valid after unlinking. No temporary paths survive
    // to graph execution, including failure, interruption and cancellation.
    for (const name of names) { try { unlinkSync(name); } catch (error) { if (error.code !== 'ENOENT') failure ??= error; } }
    try { rmdirSync(directory); } catch (error) { if (error.code !== 'ENOENT') failure ??= error; }
  }
  if (failure) {
    for (const pipe of pipes) { closeSync(pipe.read); closeSync(pipe.write); }
    throw failure;
  }
  return pipes;
}

// Node/libuv can silently invoke /bin/sh for ENOEXEC even with shell:false.
// Reject unrecognized files instead of treating ordinary text as shell code.
// ponytail: format preflight is not an OS sandbox or an atomic exec guarantee.
function argvExecutable(name, childEnv, cwd = process.cwd()) {
  if (process.platform === 'win32') return name;
  const candidates = name.includes('/') ? [resolve(cwd, name)] : (childEnv.PATH ?? '/bin:/usr/bin').split(':').map((part) => resolve(cwd, part || '.', name));
  for (const candidate of candidates) {
    let fd;
    try {
      accessSync(candidate, fsConstants.X_OK);
      fd = openSync(candidate, 'r');
      const header = Buffer.alloc(4);
      if (readSync(fd, header, 0, 4, 0) < 2) throw new Error('invalid executable');
      const magic = header.readUInt32BE(0);
      if (header.subarray(0, 2).toString() === '#!' || header.subarray(0, 2).toString() === 'MZ' || magic === 0x7f454c46 || [0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, 0xcafebabe].includes(magic)) return candidate;
      // ENOEXEC terminates PATH search, as in the native direct-exec host.
      throw Object.assign(new Error('unrecognized executable format'), { code: 'ENOEXEC' });
    } catch (error) {
      if (!['ENOENT', 'ENOTDIR', 'EACCES'].includes(error.code)) throw error;
    } finally {
      if (fd !== undefined) closeSync(fd);
    }
  }
  throw new Error('executable unavailable');
}

// runProcess runs a recipe on the host, streaming stdout/stderr back to the
// engine as process events. It honors --timeout (killing an overrunning child)
// and --retry (rerunning a failed attempt) before reporting a terminal status.
// The terminal report carries the wait status and captured output so the engine
// reconstructs the native completion value and failure cause.
async function runProcess(module, instance, script, context, request, rawRequest) {
  let attempt = 0;
  for (;;) {
    const outcome = await runProcessAttempt(module, instance, script, context, request, rawRequest);
    if (outcome.ok) {
      module.processTerminal(instance, 0, 0, 0, outcome.stdout, outcome.stderr, '', '', request);
      return;
    }
    if (outcome.retryable && attempt < context.retryCount) {
      attempt++;
      continue;
    }
    module.processTerminal(instance, outcome.outcome, outcome.status, outcome.signal, outcome.stdout, outcome.stderr, outcome.code, outcome.message, request);
    return;
  }
}

function runProcessAttempt(module, instance, script, context, request, rawRequest) {
  const retain = module.exports.kame_wasm_process_retain_limit(instance, request ?? 0n);
  if (retain === 0xffffffff) throw new Error('process retention budget unavailable');
  return new Promise((resolveAttempt, rejectAttempt) => {
    const shell = context.shell.length !== 0 ? context.shell : defaultShell();
    const childEnv = context.environmentExact ? {} : { ...process.env };
    for (const entry of context.environment) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const child = spawn(shell[0], [...shell.slice(1), script], { stdio: ['ignore', 'pipe', 'pipe'], env: childEnv, cwd: context.cwd ?? process.cwd(), detached: true });
    track(child, undefined, context.finalizing);
    const out = [];
    const err = [];
    let outLength = 0;
    let errLength = 0;
    let failure = null;
    let publicationFailure = null;
    let timedOut = false;
    let timer = null;
    let killCompletion = Promise.resolve();
    const terminate = () => { killCompletion = Promise.resolve(signalProcessTree(child, 'SIGKILL')); };
    if (context.timeoutMS > 0) {
      timer = setTimeout(() => { timedOut = true; terminate(); syncOutputReaders(); }, context.timeoutMS);
    }
    const cancelled = () => { terminate(); syncOutputReaders(); };
    const publish = (callback) => {
      if (publicationFailure !== null) return;
      try { callback(); }
      catch (error) { publicationFailure = error; cancelled(); }
    };
    context.signal?.addEventListener('abort', cancelled, { once: true });
    if (context.signal?.aborted) cancelled();
    publishReader(child.stdout, (chunk) => publish(() => {
      const kept = Math.min(chunk.length, retain + 1 - outLength);
      if (kept > 0) { out.push(Buffer.from(chunk.subarray(0, kept))); outLength += kept; }
      module.processStream(instance, false, chunk, request);
      module.drainEvents(instance, context);
    }), () => publicationFailure !== null || timedOut || context.signal?.aborted);
    publishReader(child.stderr, (chunk) => publish(() => {
      const kept = Math.min(chunk.length, retain + 1 - errLength);
      if (kept > 0) { err.push(Buffer.from(chunk.subarray(0, kept))); errLength += kept; }
      module.processStream(instance, true, chunk, request);
      module.drainEvents(instance, context);
    }), () => publicationFailure !== null || timedOut || context.signal?.aborted);
    child.once('error', (error) => { failure = error; });
    child.once('close', async (code, signalName) => {
      if (timer !== null) clearTimeout(timer);
      await killCompletion;
      context.signal?.removeEventListener('abort', cancelled);
      if (publicationFailure !== null) { rejectAttempt(publicationFailure); return; }
      const stdout = Buffer.concat(out);
      const stderr = Buffer.concat(err);
      let status = 0;
      let signal = 0;
      if (code !== null) status = code;
      if (signalName !== null) signal = osConstants.signals[signalName] ?? 0;
      if (context.signal?.aborted) resolveAttempt({ ok: false, outcome: 2, status: 0, signal, code: 'EXEC_CANCELLED', message: 'invocation cancelled', stdout, stderr, retryable: false });
      else if (timedOut) resolveAttempt({ ok: false, outcome: 1, status: 0, signal, code: 'RECIPE_TIMEOUT', message: 'recipe timed out', stdout, stderr, retryable: false });
      else if (failure !== null) resolveAttempt({ ok: false, outcome: 3, status: 0, signal: 0, code: 'HOST_FAIL', message: failure.message, stdout, stderr, retryable: false });
      else if (status !== 0 || signal !== 0) resolveAttempt({ ok: false, outcome: 0, status, signal, code: 'RECIPE_FAIL', message: `process exited with status ${status}`, stdout, stderr, retryable: true });
      else resolveAttempt({ ok: true, stdout, stderr });
    });
    publish(() => {
      module.processStarted(instance, request);
      const group = module.startedProcessGroup(instance, context, rawRequest);
      if (group) {
        group.add(child);
        child.once('close', () => group.delete(child));
      }
    });
  });
}

function effectiveGrants(inv) {
  const grants = { read: false, write: false, run: false, env: false };
  if (inv.grants && inv.grants.length !== 0) {
    for (const grant of inv.grants) {
      if (grant.capability in grants) grants[grant.capability] = true;
    }
  } else if (!inv.noDefaultGrants) {
    grants.read = true;
    grants.write = true;
    grants.run = true;
  }
  return grants;
}

function contextFor(inv) {
  const runGrants = (inv.grants ?? []).filter((grant) => grant.capability === 'run');
  return {
    portableGrants: inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }],
    toolCache: new Map(),
    grants: effectiveGrants(inv),
    runRoots: runGrants.length === 0 || runGrants.some((grant) => grant.names.length === 0) ? null : runGrants.flatMap((grant) => grant.names.map((name) => resolve(name))),
    readRoots: capabilityRoots(inv, 'read'),
    writeRoots: capabilityRoots(inv, 'write'),
    shell: inv.shell ?? [],
    environment: inv.environment ?? [],
    json: inv.json === true,
    timeoutMS: inv.timeoutMS ?? 0,
    captureLimit: inv.captureLimit ?? 0,
    retryCount: inv.retryCount ?? 0,
    logLimit: inv.logLimit ?? 0,
  };
}

function capabilityRoots(inv, capability) {
  const grants = (inv.grants ?? []).filter((grant) => grant.capability === capability);
  if ((inv.grants ?? []).length === 0 && !inv.noDefaultGrants) return [process.cwd()];
  if (grants.some((grant) => grant.names.length === 0)) return null;
  return grants.flatMap((grant) => grant.names.map((name) => resolve(name)));
}

function pathGranted(name, roots) {
  return roots == null || roots.some((root) => name === root || name.startsWith(root.endsWith('/') ? root : `${root}/`));
}

function applyDirectory(inv) {
  if (inv.directory && inv.directory !== '.') {
    try {
      process.chdir(inv.directory);
    } catch (error) {
      throw Object.assign(new Error(`cannot use working directory: ${inv.directory}`), { code: 'FS_ERR' });
    }
  }
}

async function discoverSource(inv) {
  if (inv.command) return { name: inv.sourceName ?? '<command>', text: inv.command };
  if (inv.file) {
    try {
      return { name: inv.sourceName ?? inv.file, text: await readFile(inv.file, 'utf8') };
    } catch (error) {
      throw Object.assign(new Error(`cannot read file: ${error.message}`), { code: 'FS_ERR' });
    }
  }
  for (const candidate of ['Makefile.kmk', 'make.kmk', 'src/kmk/main.kmk']) {
    if (existsSync(candidate)) return { name: candidate, text: await readFile(candidate, 'utf8') };
  }
  return null;
}

function usageError(code, message) {
  diagnostic(code, message);
  return 2;
}

function featureUnsupported(what) {
  diagnostic('FEATURE_UNSUP', `${what} requires a later stage`);
  return 1;
}

function failure(code, message) {
  diagnostic(code, message);
  return 1;
}

async function runParse(module, inv) {
  let name = '<stdin>';
  let text = '';
  if (inv.inputs[0].kind === 'command') {
    name = '<command:1>';
    text = inv.inputs[0].value;
  } else if (inv.file) {
    try {
      text = await readFile(inv.file, 'utf8');
    } catch (error) {
      return failure('FS_ERR', `cannot read source: ${inv.file}`);
    }
    name = isAbsolute(inv.file) || inv.directory === '.' ? inv.file : `${inv.directory}/${inv.file}`;
  } else {
    text = await readStdin();
  }
  const bytes = await module.parse(inv.lang, name, text);
  const document = JSON.parse(new TextDecoder().decode(bytes));
  if (document.diagnostics?.some((entry) => entry.severity === 'error')) {
    primarySource = { name, text };
    throw Object.assign(new Error('source has errors'), { diagnostics: document.diagnostics.map((entry) => ({ code: entry.code, severity: entry.severity, message: entry.message, source: name, span: entry.span })) });
  }
  if (!inv.check) writeReport(`parse ${name} · ${inv.lang}`, bytes);
  return 0;
}

async function runSession(module, inv, sourceDirectory) {
  const fragments = [];
  const selection = { defines: [...inv.defines, ...inv.parameters], environment: [...Object.entries(process.env).map(([name, value]) => `${name}=${value}`), ...inv.environment], parts: [] };
  for (let i = 0; i < inv.inputs.length; i++) {
    const input = inv.inputs[i];
    let name = `<command:${i + 1}>`, text = input.value;
    const fileBacked = input.kind === 'file' || input.kind === 'discover';
    if (input.kind === 'discover') {
      const source = await discoverSource({});
      if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found (tried Makefile.kmk, make.kmk, src/kmk/main.kmk)');
      name = normalize(join(inv.directory || '.', source.name));
      text = source.text;
    } else if (input.kind === 'file') {
      name = isAbsolute(input.value) ? normalize(input.value) : normalize(join(inv.directory || '.', input.value));
      try { text = await readFile(resolve(sourceDirectory, name), 'utf8'); }
      catch { return failure('FS_ERR', `cannot read source: ${input.value}`); }
    } else if (input.kind === 'stdin') { name = '<stdin>'; text = await readStdin(); }
    sourceTexts.set(name, text);
    if (input.lang === 'km' || input.lang === 'kmk') {
      const parts = await expandSessionIncludes(module, sourceDirectory, name, text, input.lang, [], fileBacked, true, selection);
      for (let j = 0; j < parts.length; j++) fragments.push({ ...parts[j], lang: input.lang, entries: j + 1 === parts.length ? input.entries : [], inline: fileBacked && j + 1 === parts.length ? 0 : 1, skipStatements: input.entries.length ? 1 : 0 });
    } else fragments.push({ name, text, lang: input.lang, entries: input.entries, inline: fileBacked ? 0 : 1, comment: inv.comment, defines: inv.defines, check: inv.check ? 1 : 0 });
  }
  if (inv.inputs.length === 1 && inv.inputs[0].lang === 'kmk' && fragments.length === 1 && !inv.timeoutMS) {
    const input = inv.inputs[0];
    if (input.kind !== 'stdin') return runPrimary(module, { ...inv, name: '', sourceName: input.kind === 'command' ? '<command:1>' : fragments[0].name, file: input.kind === 'file' ? resolve(sourceDirectory, fragments[0].name) : '', command: input.kind === 'command' ? input.value : '', targets: input.entries }, false, sourceDirectory);
  }
  buildProgress = { active: 0, completed: 0, failed: 0, cancelled: 0 };
  buildStartedAt = performance.now();
  return module.runSession(fragments, inv, contextFor(inv));
}

async function discoverBuildSource(module, inv, sourceDirectory, watchedSources = null) {
  if (watchedSources && inv.file) watchedSources.add(resolve(inv.file));
  if (watchedSources && !inv.file && !inv.command) {
    const candidates = ['Makefile.kmk', 'make.kmk', 'src/kmk/main.kmk'];
    for (const candidate of candidates) {
      const path = resolve(process.cwd(), candidate);
      watchedSources.add(path);
      if (existsSync(path)) break;
    }
  }
  const source = await discoverSource(inv);
  if (source === null) return null;
  const name = inv.sourceName ?? (inv.command ? source.name : isAbsolute(source.name) ? normalize(source.name) : normalize(join(inv.directory || '.', source.name)));
  watchedSources?.add(resolve(sourceDirectory, name));
  const environment = [...Object.entries(process.env).map(([name, value]) => `${name}=${value}`), ...inv.environment];
  const selection = { defines: [...inv.defines, ...inv.parameters], environment, parts: [], watchedSources };
  const parts = await expandSessionIncludes(module, sourceDirectory, name, source.text, 'kmk', [], !inv.command, true, selection);
  parts.defines = inv.defines;
  parts.parameters = inv.parameters;
  parts.toolOverrides = inv.toolOverrides;
  parts.force = inv.force ? 1 : 0;
  parts.environment = environment;
  const compiled = parts.length === 1 && !inv.defines.length && !inv.parameters.length && !inv.toolOverrides.length && !environment.length && !inv.force ? source.text : parts;
  // Tools has a command operand; list has a search query, not a build root.
  // Neither should normalize operands or implicitly select default.
  const catalogue = inv.name === 'tools' || inv.name === 'list';
  const targets = catalogue ? inv.targets : await module.targetOperands(compiled, inv.targets, name);
  if (!catalogue && targets.length === 0) throw Object.assign(new Error('no target was requested and no default target is defined'), { code: 'TGT_NO_DEFAULT' });
  return { name, text: source.text, compiled, targets };
}

// Syntax and byte spans come from the portable parser, not a second JS grammar.
async function expandSessionIncludes(module, sourceDirectory, name, text, lang, active = [], fileBacked = true, validate = true, selection = { defines: [], environment: [], parts: [] }, includes = []) {
  const identity = resolve(sourceDirectory, name);
  selection.watchedSources?.add(identity);
  sourceTexts.set(name, text);
  if (active.includes(identity)) throw Object.assign(new Error(`include cycle: ${name}`), { code: 'DEP_CYCLE' });
  const document = JSON.parse(new TextDecoder().decode(await module.parse(lang === 'kmk' ? 'script' : lang, name, text)));
  const invalid = document.diagnostics.find((item) => item.severity === 'error');
  if (invalid) {
    primarySource = { name, text };
    throw Object.assign(new Error(invalid.message), { code: invalid.code, span: invalid.span, diagnostics: document.diagnostics.filter((item) => item.severity === 'error').map((item) => ({ ...item, source: name })) });
  }
  const bytes = new TextEncoder().encode(text);
  const parts = [];
  const slice = (span) => new TextDecoder().decode(bytes.subarray(span.start, span.end));
  const append = (end) => {
    const part = { name, text: new TextDecoder().decode(bytes.subarray(start, end)), offset: start, ...(includes.length ? { includes } : {}) };
    parts.push(part);
    selection.parts.push(document.ast.items.filter((item) => item.kind === 'definition' && item.span.start >= start && item.span.end <= end).map((item) => new TextDecoder().decode(bytes.subarray(item.span.start, item.span.end))).join('\n'));
  };
  const include = async (path, optional, span) => {
    if (!fileBacked) {
      primarySource = { name, text };
      const detail = { code: 'FEATURE_UNSUP', severity: 'error', message: `include requires a file-backed source; this fragment was supplied ${name === '<stdin>' ? 'on stdin' : 'inline'}`, source: name, span, tips: ['save this fragment as a .km or .kmk file and run that file'] };
      throw Object.assign(diagnosticError(`${detail.code}: ${detail.message}`, detail.code), { span, diagnostics: [detail] });
    }
    const included = normalize(isAbsolute(path) ? path : join(dirname(name), path));
    selection.watchedSources?.add(resolve(sourceDirectory, included));
    if ([...active, identity].includes(resolve(sourceDirectory, included))) {
      primarySource = { name, text };
      const detail = { code: 'DEP_CYCLE', severity: 'error', message: `include cycle: ${included}`, source: name, span };
      throw Object.assign(diagnosticError(`${detail.code}: ${detail.message}`, detail.code), { span, diagnostics: [detail] });
    }
    let child;
    try { child = await readFile(resolve(sourceDirectory, included), 'utf8'); }
    catch (error) {
      if (optional && error.code === 'ENOENT') return;
      primarySource = { name, text };
      const detail = { code: 'FS_ERR', severity: 'error', message: `cannot read included source: ${included}`, source: name, span };
      throw Object.assign(diagnosticError(`${detail.code}: ${detail.message}`, detail.code), { span, diagnostics: [detail] });
    }
    parts.push(...await expandSessionIncludes(module, sourceDirectory, included, child, lang, [...active, identity], true, validate, selection, [...includes, { source: name, label: included, start: span.start, end: span.end }]));
  };
  // All branches have already been validated by the portable parser. Walk only
  // the selected application AST; never interpret source text as JS grammar.
  const compose = async (expression) => {
    if (expression.kind === 'nil') return;
    const [head, ...args] = expression.items;
    if (head.text === 'include' || head.text === 'include?') {
      const path = args[0];
      if (path.kind === 'path' && typeof path.text === 'string' && !path.parts) return include(path.text, head.text === 'include?', expression.span);
      if (path.kind === 'string' && path.parts.every((part) => typeof part.text === 'string' && !part.expression)) return include(path.parts.map((part) => part.text).join(''), head.text === 'include?', expression.span);
      throw Object.assign(new Error('invalid literal include path in validated AST'), { code: 'PARSE_ERR', span: path.span });
    }
    for (let i = 0; i + 1 < args.length; i += 2) {
      const condition = args[i];
      let selected;
      try {
        selected = await module.compositionCondition({ name, prefix: selection.parts.join('\n'), predicate: slice(condition.span), defines: selection.defines, environment: selection.environment });
      } catch (error) {
        primarySource = { name, text };
        error.span = condition.span;
        const details = error.diagnostics ?? [{ code: error.code ?? 'EXPR_INVALID', severity: 'error', message: error.message }];
        error.diagnostics = details.map((detail) => ({ ...detail, source: name, span: condition.span }));
        throw error;
      }
      if (selected) return compose(args[i + 1]);
    }
    if (args.length % 2) await compose(args.at(-1));
  };
  let start = 0;
  for (const item of document.ast.items) {
    if (item.kind !== 'include' && item.kind !== 'composition') continue;
    append(item.span.start);
    if (item.kind === 'include') await include(item.path, item.optional, item.span);
    else await compose(item.expression);
    start = item.span.end;
  }
  append(bytes.length);
  return parts;
}

async function runFmt(module, inv) {
  if (inv.files.length === 0) {
    const name = inv.inputs[0].kind === 'command' ? '<command:1>' : '<stdin>';
    const text = inv.inputs[0].kind === 'command' ? inv.inputs[0].value : await readStdin();
    const bytes = await module.format(inv.lang, name, text, inv.indent, inv.indentWidth, inv.comment);
    const changed = Buffer.from(bytes).toString('utf8') !== text;
    const action = inv.check ? 'check' : 'format';
    if (jsonMode) writeDataResult('format-result', { source: name, action, changed }, bytes, action === 'format');
    else if (inv.check) {
      if (changed) stdout.write(`${name}\n`);
      stderr.write(`${styled('status.success', 'done')} fmt ${name} · ${changed ? 'would change' : 'unchanged'}\n`);
    } else stdout.write(bytes);
    formatCounts.completed++;
    return inv.check && changed ? 1 : 0;
  }
  let different = false;
  for (const file of inv.files) {
    const name = isAbsolute(file) || inv.directory === '.' ? file : `${inv.directory}/${file}`;
    let text;
    try {
      text = await readFile(file, 'utf8');
    } catch (error) {
      return failure('FS_ERR', `cannot read source: ${file}`);
    }
    primarySource = { name, text };
    const bytes = await module.format(inv.lang, name, text, inv.indent, inv.indentWidth, inv.comment);
    const changed = new TextDecoder().decode(bytes) !== text;
    if (inv.check && changed) different = true;
    if (inv.inPlace && changed) {
      try {
        await writeFileAtomic(file, bytes);
      } catch (error) {
        return failure('FS_ERR', `cannot replace source: ${file}`);
      }
    }
    const action = inv.check ? 'check' : inv.inPlace ? 'in-place' : 'format';
    if (jsonMode) writeDataResult('format-result', { source: name, action, changed }, bytes, action === 'format');
    else if (action === 'format') stdout.write(bytes);
    else {
      if (inv.check && changed) stdout.write(`${name}\n`);
      stderr.write(`${styled('status.success', 'done')} fmt ${name} · ${changed ? inv.check ? 'would change' : 'formatted' : 'unchanged'}\n`);
    }
    formatCounts.completed++;
  }
  return different ? 1 : 0;
}

async function runPlan(module, inv, sourceDirectory) {
  return runGraph(module, inv, sourceDirectory);
}

async function runList(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  let context;
  if (!jsonMode) {
    context = contextFor(inv);
    context.inspection = true;
    context.grants.write = false;
    context.grants.run = false;
    context.inspectionGrants = context.portableGrants;
  }
  const bytes = await module.graphJSON(source.compiled, inv.targets[0] || '', 0, jsonMode ? graphKinds.list : graphKinds.listReport, false, source.name, context);
  writeReport('list', JSON.parse(Buffer.from(bytes).toString('utf8')));
  return 0;
}

async function runGraph(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = source.targets;
  if (inv.name !== 'plan' && targets.length !== 1) return usageError('OPT_VALUE_INVALID', `${inv.name} requires exactly one target`);
  const kind = inv.name === 'plan' ? graphKinds.planRoots : graphKinds[inv.name];
  let context;
  if (inv.expand || inv.name !== 'span') {
    context = contextFor(inv);
    context.inspection = true;
    context.grants.write = false;
    context.grants.run = false;
    // Match the build context's proof identity. Resolving-phase evaluation and
    // the host's inspection policy still prohibit writes and process execution.
    context.inspectionGrants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }];
  }
  const queryTarget = inv.name === 'plan' ? JSON.stringify(targets) : targets[0];
  const bytes = await module.graphJSON(source.compiled, queryTarget, inv.depth, kind, inv.name === 'plan' ? inv.affected : inv.expand, source.name, context);
  const result = JSON.parse(Buffer.from(bytes).toString('utf8'));
  if (result.type === 'diagnostic') {
    if (jsonMode) stdout.write(`${JSON.stringify(result)}\n`);
    else stderr.write(renderDiagnostic(result.diagnostic, source, 80));
    return 1;
  }
  writeReport(`${inv.name} ${targets.join(' ')} · depth ${inv.depth}`, result);
  return 0;
}

function shortReportField(text) {
  return /[\x00-\x1f\x7f]/u.test(text) ? JSON.stringify(text).replaceAll('\x7f', '\\u007f') : text;
}

async function runTools(module, inv, sourceDirectory) {
  const check = inv.args[0] === 'check';
  let targets = inv.targets;
  if (!check && targets.length !== 0) return usageError('OPT_VALUE_INVALID', 'tools does not accept targets');
  if (check && targets.length === 0) return usageError('OPT_VALUE_INVALID', 'tools check requires at least one target');
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  if (check) {
    targets = await module.targetOperands(source.compiled, targets, source.name);
    if (targets.length === 0) return usageError('OPT_VALUE_INVALID', 'tools check requires at least one target');
    let failed = false;
    for (const target of targets) {
      const context = contextFor(inv);
      context.cliReport = true;
      context.inspection = true;
      context.grants.write = false;
      context.grants.run = false;
      context.inspectionGrants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }];
      const text = module.decode0(await module.toolsCheck(source.compiled, target, source.name, context));
      let targetFailed = false;
      for (const line of text.split('\n').filter(Boolean)) {
        const event = JSON.parse(line);
        if (event.type === 'tools-check-result') { if (inv.json) stdout.write(`${line}\n`); continue; }
        if (inv.json) stdout.write(`${line}\n`);
        else { clearDashboard(); stderr.write(renderDiagnostic(event.diagnostic, source, 80)); }
        failed = true;
        targetFailed = true;
        invocationHadDiagnostic = true;
      }
      if (!jsonMode && !targetFailed) stderr.write(`${styled('status.success', 'done')} tools check ${target}\n`);
    }
    return failed ? 1 : 0;
  }
  const names = await module.toolNames(source.compiled, source.name);
  writeReport('tools', names.map((name) => ({ name, path: resolveTool(name, inv.toolOverrides) })));
  return 0;
}

async function runCat(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = source.targets;
  if (targets.length !== 1) return usageError('OPT_VALUE_INVALID', 'cat requires exactly one target');
  const target = targets[0];
  try {
    const context = { ...contextFor(inv), api: true, events: [] };
    const { kind, bytes, events } = await module.materialize(source.compiled, target, context, source.name);
    if (kind === 1) {
      if (jsonMode) {
        const value = events.findLast((event) => event.type === 'target-value' && event.target === target);
        if (!value) throw Object.assign(new Error('typed artifact value unavailable'), { code: 'HOST_FAIL' });
        stdout.write(`${JSON.stringify(value)}\n`);
      } else {
        const value = events.findLast((event) => event.type === 'target-value' && event.target === target);
        if (!value) throw Object.assign(new Error('typed artifact value unavailable'), { code: 'HOST_FAIL' });
        const output = catHumanOutput(value.value);
        if (output === undefined) return failure('VAL_NOT_JSON', 'definition value has no JSON representation');
        stdout.write(output);
      }
      return 0;
    }
    if (kind === 2) {
      const name = new TextDecoder().decode(bytes);
      try {
        writeArtifact(target, await readFile(name));
        return 0;
      } catch (error) {
        return failure('FS_ERR', `cannot read artifact: ${error.message}`);
      }
    }
    return failure('NO_ARTIFACT', 'target has no readable artifact');
  } catch (error) {
    if (error.code === 'TGT_NO_RULE' && isPathTarget(target) && existsSync(target)) {
      writeArtifact(target, await readFile(target));
      return 0;
    }
    throw error;
  }
}

function writeArtifact(target, bytes) {
  if (!jsonMode) { stdout.write(bytes); return; }
  if (!bytes.length) writeDataResult('artifact', { target }, bytes);
  for (let start = 0; start < bytes.length; start += 32768) writeDataResult('artifact', { target }, bytes.subarray(start, start + 32768));
}

// catHumanOutput renders a typed target-value for human `do cat`: text and
// bytes stay raw so pipelines receive exact content, while every other
// JSON-native value prints as plain JSON. Values without a JSON
// representation yield undefined so the caller fails with VAL_NOT_JSON.
function catHumanOutput(typed) {
  if (!typed || typeof typed.kind !== 'string') return undefined;
  if (typed.kind === 'string') return typed.data ?? '';
  if (typed.kind === 'bytes') {
    if (typed.encoding === 'base64') return Buffer.from(typed.data ?? '', 'base64');
    return typed.data ?? '';
  }
  const plain = catPlainJSON(typed);
  if (plain === undefined) return undefined;
  return JSON.stringify(plain);
}

function catPlainJSON(typed, depth = 0) {
  if (!typed || depth >= 256) return undefined;
  switch (typed.kind) {
    case 'nil': return null;
    case 'bool': return !!typed.data;
    case 'int': return typed.data;
    case 'float': {
      const n = typed.data;
      if (typeof n !== 'number' || !Number.isFinite(n)) return undefined;
      return Object.is(n, -0) ? 0 : n;
    }
    case 'string': return typed.data ?? '';
    case 'bytes': {
      // Nested bytes only survive as UTF-8 text; binary has no plain JSON form.
      if (typed.encoding === 'base64') return undefined;
      return typed.data ?? '';
    }
    case 'list': {
      if (!Array.isArray(typed.items)) return undefined;
      const out = [];
      for (const item of typed.items) {
        const v = catPlainJSON(item, depth + 1);
        if (v === undefined) return undefined;
        out.push(v);
      }
      return out;
    }
    case 'record': {
      if (!Array.isArray(typed.fields)) return undefined;
      const out = {};
      for (const field of typed.fields) {
        const v = catPlainJSON(field.value, depth + 1);
        if (v === undefined) return undefined;
        out[field.name] = v;
      }
      return out;
    }
    default: return undefined;
  }
}

async function watchFingerprint(kind, name, metadata = false) {
  if (kind === 'glob') {
    try { return JSON.stringify(resourceWildcard(name)); } catch { return 'unavailable'; }
  }
  let content = 'not-file';
  try {
    if ((await statResource(name)).isFile()) {
      if (isMemoryResource(name) || process.platform === 'win32') {
        // ponytail: Windows retains the dispatcher's stat/read race; POSIX
        // uses a nonblocking descriptor so a replacement FIFO cannot stall.
        const bytes = await readResource(name);
        content = createHash('sha256').update(bytes).digest('hex');
      } else {
        const file = await open(filesystemPath(name), fsConstants.O_RDONLY | fsConstants.O_NONBLOCK);
        try {
          if ((await file.stat()).isFile()) content = createHash('sha256').update(await file.readFile()).digest('hex');
        } finally { await file.close(); }
      }
    }
  } catch (error) {
    content = error.code === 'ENOENT' || error.code === 'ENOTDIR' ? 'missing' : `error:${error.code ?? error.message}`;
  }
  if (!metadata) return content;
  const views = [content];
  for (const noFollow of [false, true]) {
    try {
      const info = await statResource(name, undefined, noFollow);
      views.push({ size: info.size, mode: info.mode, dir: info.isDirectory(), link: noFollow && info.isSymbolicLink() });
    } catch (error) { views.push(`error:${error.code ?? error.message}`); }
  }
  try { views.push(isMemoryResource(name) ? null : (await readlink(filesystemPath(name), { encoding: 'buffer' })).toString('hex')); }
  catch (error) { views.push(`error:${error.code ?? error.message}`); }
  return JSON.stringify(views);
}

function watchKey(kind, name) { return `${kind}\0${name}`; }

async function watchProbe(name) {
  // Windows ctime is creation time, not a reliable change timestamp.
  if (process.platform === 'win32' || isMemoryResource(name)) return null;
  const views = [];
  for (const noFollow of [false, true]) {
    try {
      const info = await statResource(name, { bigint: true }, noFollow);
      views.push([info.dev, info.ino, info.mode, info.size, info.mtimeNs, info.ctimeNs].map(String));
    } catch (error) {
      if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') return null;
      views.push('missing');
    }
  }
  return JSON.stringify(views);
}

async function scanWatchFingerprint(record) {
  if (record.source || record.kind !== 'file') return watchFingerprint(record.kind, record.name, record.metadata);
  const probe = await watchProbe(record.name);
  if (probe !== null && probe === record.probe && record.probeMetadata === !!record.metadata) return record.stamp;
  const stamp = await watchFingerprint(record.kind, record.name, record.metadata);
  const after = await watchProbe(record.name);
  record.probe = probe !== null && probe === after ? probe : null;
  record.probeMetadata = !!record.metadata;
  return stamp;
}

function reportWatchError(error, source) {
  if (error.diagnostics?.length) {
    for (const detail of error.diagnostics) {
      if (jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
      else stderr.write(renderDiagnostic(detail, source, 80));
    }
    return;
  }
  const detail = lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
  if (jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
  else stderr.write(renderDiagnostic(detail, source, 80));
}

// CLI-only subscriptions. The embedded API retains explicit invalidation for
// custom hosts. Directory events are hints; existing fingerprints decide changes.
class WatchNotifications {
  constructor(signal) {
    this.signal = signal;
    this.watchers = [];
    this.dirty = true;
    this.complete = false;
    this.wake = null;
    this.abort = () => this.wake?.();
    signal.addEventListener('abort', this.abort);
  }

  changed() { this.dirty = true; this.wake?.(); }

  sync(records) {
    const directories = new Set();
    const traversed = new Set();
    let complete = true;
    const add = (directory, recursive = false, depth = 0) => {
      if (depth > 128) { complete = false; return; }
      directory = resolve(directory);
      while (true) {
        try { if (statSync(directory).isDirectory()) break; }
        catch (error) { if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') complete = false; }
        const parent = dirname(directory);
        if (parent === directory) { complete = false; return; }
        directory = parent;
        recursive = false;
      }
      directories.add(directory);
      if (!recursive || traversed.has(directory)) return;
      // ponytail: cover the whole glob-root subtree; prune by pattern if watch
      // limits or traversal cost become material. Failed coverage enables polling.
      traversed.add(directory);
      try {
        for (const entry of readdirSync(directory, { withFileTypes: true })) {
          if (entry.isDirectory()) add(join(directory, entry.name), true, depth + 1);
        }
      } catch { complete = false; }
    };
    for (const record of records) {
      if (isMemoryResource(record.name)) { complete = false; continue; }
      const rawName = filesystemPath(record.name);
      const name = process.platform === 'win32' ? rawName.replaceAll('\\', '/') : rawName;
      if (record.kind === 'glob') {
        const root = globRoot(name);
        add(root === name ? dirname(name) : root, true);
      } else {
        add(dirname(name));
        try { add(dirname(realpathSync(name))); } catch {}
        // Also observe creation of a dangling symlink's target.
        try { add(dirname(resolve(dirname(name), readlinkSync(name)))); } catch {}
      }
    }
    const next = [];
    for (const directory of directories) {
      try {
        const watcher = fsWatch(directory, () => this.changed());
        watcher.on('error', () => { this.complete = false; this.changed(); watcher.close(); });
        next.push(watcher);
      } catch { complete = false; }
    }
    for (const watcher of this.watchers) watcher.close();
    this.watchers = next;
    this.complete = complete;
  }

  async wait(milliseconds) {
    if (this.signal.aborted) return;
    // Yield to I/O even when another scan is already queued (host requests and
    // signal callbacks must not be starved by a chain of resolved promises).
    if (this.dirty) milliseconds = 0;
    await new Promise((done) => {
      const timer = setTimeout(finish, milliseconds);
      const self = this;
      function finish() { clearTimeout(timer); self.wake = null; done(); }
      this.wake = finish;
    });
  }

  close() {
    for (const watcher of this.watchers) watcher.close();
    this.watchers = [];
    this.signal.removeEventListener('abort', this.abort);
    this.wake?.();
  }
}

async function runPrimaryWatch(module, inv, noArguments, sourceDirectory) {
  const cancellation = new AbortController();
  invocationCancellations.add(cancellation);
  let finishDisposal;
  const disposal = new Promise((resolveDisposal) => { finishDisposal = resolveDisposal; });
  invocationDisposals.add(disposal);
  const context = contextFor(inv);
  const notifications = new WatchNotifications(cancellation.signal);
  context.human = inv.json !== true;
  const watchedSources = new Set();
  let source = null;
  let watch = null;
  let fingerprints = new Map();
  let pending = new Map();
  let lastScan = 0;
  let lastChange = 0;
  let reported = new Set();
  let status = 0;
  let cycle = 0, cycleFinished = false, cycleFailed = false, cycleStarted = performance.now();
  const startCycle = () => {
    clearDashboard(); cycle++; cycleStarted = performance.now(); cycleFinished = false; cycleFailed = false; watchIdle = false;
    watchCycle = cycle;
    buildProgress = { active: 0, completed: 0, failed: 0, cancelled: 0 }; terminalOutcomes.clear(); startedOutcomes.clear();
    buildStartedAt = cycleStarted;
    if (jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'watch-cycle-started', cycle })}\n`);
  };
  const finishCycle = () => {
    if (cycleFinished) return;
    clearDashboard();
    const outcome = cycleFailed || buildProgress.failed ? 'failure' : 'success';
    watchCycleStatus = outcome; watchCycleElapsed = Math.max(0, Math.floor(performance.now() - cycleStarted));
    if (jsonMode) {
      stdout.write(`${JSON.stringify({ schema: 1, type: 'watch-cycle-finished', cycle, status: outcome, elapsedMS: Math.max(0, Math.floor(performance.now() - cycleStarted)), completed: buildProgress.completed, failed: buildProgress.failed, cancelled: buildProgress.cancelled })}\n`);
      stdout.write(`${JSON.stringify({ schema: 1, type: 'watch-idle', cycle, status: outcome })}\n`);
    } else {
      printSummary(buildProgress, outcome === 'failure' ? 1 : 0, watchCycleElapsed, `watch cycle ${cycle}`);
      stderr.write(`watch · waiting for changes · last cycle ${outcome} · Ctrl+C to stop\n`);
    }
    cycleFinished = true; watchIdle = true;
  };
  if (!inv.json) stderr.write('Watching filesystem resources (OS notifications; 10s reconciliation; 200ms polling fallback; 100ms debounce)\n');

  const seedSources = async () => {
    for (const name of watchedSources) {
      const key = watchKey('file', name);
      if (!fingerprints.has(key)) {
        notifications.changed();
        fingerprints.set(key, { kind: 'file', name, stamp: await watchFingerprint('file', name), source: true });
      }
    }
  };
  const compile = async () => {
    watchedSources.clear();
    startCycle();
    try {
      source = await discoverBuildSource(module, inv, sourceDirectory, watchedSources);
      if (source === null) {
        cycleFailed = true;
        if (!noArguments) reportWatchError(Object.assign(new Error('no build source found'), { code: 'BUILD_NO_SOURCE' }), null);
        await seedSources();
        return false;
      }
      primarySource = source;
      lastDiagnostic = null;
      const targets = source.targets;
      watch = await module.beginWatch(source.compiled, targets, source.name, { ...context, signal: cancellation.signal });
      cancellation.signal.addEventListener('abort', () => watch?.cancellation.abort(), { once: true });
      invocationCancellations.add(watch.cancellation);
      const snapshot = module.watchSnapshot(watch);
      await syncWatchResources(snapshot);
      await seedSources();
      return true;
    } catch (error) {
      cycleFailed = true;
      reportWatchError(error, source);
      await seedSources();
      return false;
    }
  };
  const syncWatchResources = async (snapshot) => {
    const active = new Set();
    for (const resource of snapshot.resources ?? []) {
      const key = watchKey(resource.kind, resource.name);
      active.add(key);
      const tracked = fingerprints.get(key);
      // The resource node can appear before the suspended operation accepts its
      // metadata/link observation. Promote the live watch when that view arrives.
      if (tracked && resource.metadata && !tracked.metadata) {
        notifications.changed();
        tracked.metadata = true;
        tracked.stamp = 'unobserved';
      }
      if (!tracked || tracked.stamp === null) {
        notifications.changed();
        const stamp = resource.metadata ? 'unobserved' : resource.missing === true ? 'missing' : resource.signature ?? await watchFingerprint(resource.kind, resource.name);
        if (!tracked) fingerprints.set(key, { ...resource, stamp, source: false });
        else tracked.stamp = stamp;
      }
    }
    for (const [key, record] of fingerprints) {
      if (!record.source && !active.has(key)) { fingerprints.delete(key); notifications.changed(); }
    }
  };

  try {
    let active = await compile();
    while (!cancellation.signal.aborted) {
      let busy = false;
      if (active) {
        await module.waitReady(watch.instance, watch.context);
        const step = module.drainWatch(watch);
        if (step === 1) module.serviceWatchRequest(watch);
        if (watch.hostError) throw watch.hostError;
        const snapshot = module.watchSnapshot(watch);
        busy = snapshot.busy;
        await syncWatchResources(snapshot);
        for (const root of snapshot.roots ?? []) {
          if (!root.done || !root.diagnosticJSON) continue;
          const key = `${root.index}:${root.revision}:${root.generation}`;
          if (reported.has(key)) continue;
          reported.add(key);
          cycleFailed = true;
          const encoded = JSON.parse(root.diagnosticJSON);
          const detail = encoded.diagnostic ?? encoded;
          if (jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
          else stderr.write(renderDiagnostic(detail, source, 80));
        }
        if (!snapshot.busy) finishCycle();
      }
      if (!active) finishCycle();

      const now = Date.now();
      if (notifications.dirty || now - lastScan >= (notifications.complete ? 10000 : 200)) {
        await seedSources();
        notifications.dirty = false;
        notifications.sync(fingerprints.values());
        for (const [key, record] of fingerprints) {
          const stamp = await scanWatchFingerprint(record);
          if (record.stamp === null) { record.stamp = stamp; continue; }
          if (stamp !== record.stamp) {
            clearDashboard();
            if (!jsonMode) stderr.write(`info watch: change queued: ${record.name}\n`);
            record.stamp = stamp;
            pending.set(key, { kind: record.kind, name: record.name });
            if (record.source) pending.set(`source\0${record.name}`, { kind: 'source', name: record.name });
            lastChange = now;
          }
        }
        lastScan = now;
      }

      if (pending.size && now - lastChange >= 100 && (!active || !module.watchSnapshot(watch).busy)) {
        const sourceChanged = [...pending.values()].some((item) => item.kind === 'source');
        if (sourceChanged && active) {
          invocationCancellations.delete(watch.cancellation);
          await module.disposeWatch(watch);
          watch = null;
          active = false;
        }
        if (sourceChanged || !active) {
          fingerprints = new Map([...fingerprints].filter(([, record]) => record.source));
          reported = new Set();
          pending.clear();
          active = await compile();
          lastScan = Date.now();
        } else {
          startCycle();
          const resources = [...pending.values()].filter((item) => item.kind === 'file' || item.kind === 'glob');
          if (resources.length) module.invalidateWatch(watch, resources);
          pending.clear();
          reported = new Set();
        }
        notifications.changed();
      }
      const interval = notifications.complete ? 10000 : 200;
      const scanDue = Math.max(1, interval - (Date.now() - lastScan));
      await notifications.wait(Math.min(busy ? 10 : 1000, pending.size ? 100 : scanDue, scanDue));
    }
  } catch (error) {
    if (!cancellation.signal.aborted) {
      reportWatchError(error, source);
      status = 1;
    } else if (interruptedStatus) status = interruptedStatus;
  } finally {
    notifications.close();
    if (watch) {
      invocationCancellations.delete(watch.cancellation);
      try { await module.disposeWatch(watch); } catch { status = status || 1; }
    }
    invocationCancellations.delete(cancellation);
    invocationDisposals.delete(disposal);
    finishDisposal();
  }
  return interruptedStatus || status;
}

async function runPrimary(module, inv, noArguments, sourceDirectory) {
  if (inv.watch) return runPrimaryWatch(module, inv, noArguments, sourceDirectory);
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) {
    if (noArguments) {
      const help = await module.parseCLI('@help', ['--output', outputMode]);
      stdout.write(help.help);
      return 0;
    }
    return failure('BUILD_NO_SOURCE', 'no build source found');
  }
  primarySource = source;
  buildProgress = { active: 0, completed: 0, failed: 0, cancelled: 0 };
  buildStartedAt = performance.now();
  const context = contextFor(inv);
  context.human = inv.json !== true;
  const targets = source.targets;
  if (inv.dryRun) {
    const parts = Array.isArray(source.compiled) ? source.compiled : source.compiled?.parts;
    const sourceParts = parts?.length ? parts : [{ name: source.name, text: source.text }];
    const fragments = sourceParts.map((part, index) => ({
      name: part.name ?? source.name,
      text: part.text,
      lang: 'kmk',
      entries: index + 1 === sourceParts.length ? targets : [],
      inline: index + 1 === sourceParts.length ? 0 : 1,
    }));
    return module.runSession(fragments, inv, context);
  }
  let failed = false;
  try {
    lastDiagnostic = null;
    const results = await module.materializeMany(source.compiled, targets, context, source.name);
    for (const { kind, bytes } of results) if (kind === 1 && !inv.json) stdout.write(bytes);
  } catch (error) {
    if (inv.json === true) throw error;
    if (error.diagnostics) {
      for (const detail of error.diagnostics) stderr.write(renderDiagnostic(detail, primarySource, 80));
      return 1;
    }
    failed = true;
    const detail = error.diagnostics?.[0] ?? lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
    if (!error.diagnostics && !lastDiagnostic && error.span !== undefined) { detail.source = source.name; detail.span = error.span; }
    if (!detail.target && detail.code !== 'PARSE_ERR') detail.target = targets[0];
    stderr.write(renderDiagnostic(detail, primarySource, 80));
  }
  return failed ? 1 : 0;
}

async function dispatch(module, inv, noArguments) {
  configurePresentation(inv);
  if (noArguments && !['Makefile.kmk', 'make.kmk', 'src/kmk/main.kmk'].some((name) => existsSync(name))) {
    const help = await module.parseCLI('@help', ['--output', outputMode]);
    stdout.write(help.help); return 0;
  }
  presentationCommand = inv.name || 'build';
  presentationSubject = inv.targets[0] || (inv.inputs?.[0]?.kind === 'file' ? inv.inputs[0].value : inv.inputs?.length ? '<command:1>' : 'default');
  invocationHadDiagnostic = false;
  cliWorkers = []; terminalOutcomes = new Set(); startedOutcomes = new Set(); dashboardRows = 0; dashboardUnsafe = false; dashboardPending = false; dashboardLast = 0;
  watchIdle = false;
  watchCycle = 0; watchCycleStatus = ''; watchCycleElapsed = 0;
  formatCounts = { completed: 0, failed: 0, cancelled: 0 };
  const started = performance.now();
  if (inv.dryRun && !jsonMode) stderr.write(`info ${presentationCommand}: dry-run · no effects or processes\n`);
  const streaming = ['', 'run', 'render', 'cat', 'fmt'].includes(inv.name) || (inv.name === 'cache' && inv.args[0] === 'clean') || (inv.name === 'tools' && inv.args[0] === 'check');
  if (streaming && jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'invocation-started', command: presentationCommand, ...(inv.dryRun ? { dryRun: true } : {}) })}\n`);
  let status;
  try { status = await dispatchCommand(module, inv, noArguments); }
  catch (error) {
    invocationHadDiagnostic = true; clearDashboard();
    const details = error.diagnostics ?? [lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message, ...(error.span !== undefined && primarySource ? { source: primarySource.name, span: error.span } : {}) }];
    if (jsonMode) {
      const records = details.map((detail) => ({ schema: 1, type: 'diagnostic', diagnostic: detail }));
      if (streaming || inv.name === 'plan') for (const record of records) stdout.write(`${JSON.stringify(record)}\n`);
      else stdout.write(`${JSON.stringify(records.length === 1 ? records[0] : records)}\n`);
    } else for (const detail of details) stderr.write(renderDiagnostic(detail, primarySource, diagnosticFormat === 'human' ? stderr.columns || 80 : 80));
    status = interruptedStatus || 1;
  }
  clearDashboard();
  if (inv.name === 'fmt' && status && invocationHadDiagnostic) formatCounts.failed++;
  if (streaming && jsonMode) {
    const outcome = interruptedStatus || (buildProgress?.cancelled && !buildProgress.failed && status) ? 'cancelled' : status === 0 ? 'success' : inv.name === 'fmt' && !invocationHadDiagnostic ? 'different' : 'failure';
    const counts = inv.name === 'fmt' ? formatCounts : ['', 'run'].includes(inv.name) ? { completed: buildProgress?.completed ?? 0, failed: buildProgress?.failed ?? 0, cancelled: buildProgress?.cancelled ?? 0 } : {};
    stdout.write(`${JSON.stringify({ schema: 1, type: 'summary', command: presentationCommand, status: outcome, exitStatus: status, elapsedMS: Math.max(0, Math.floor(performance.now() - started)), ...counts })}\n`);
  }
  const humanCounts = inv.name === 'fmt' ? formatCounts : buildProgress;
  if (!jsonMode && (inv.name !== 'fmt' || inv.check || (inv.inPlace && inv.files.length)) && humanCounts && humanCounts.completed + humanCounts.failed + humanCounts.cancelled !== 0) printSummary(humanCounts, status, performance.now() - started);
  return status;
}

async function dispatchCommand(module, inv, noArguments) {
  lastDiagnostic = null;
  buildProgress = null;
  primarySource = null;
  sourceTexts.clear();
  const sourceDirectory = process.cwd();
  applyDirectory(inv);
  if (inv.name === 'help') {
    const topic = inv.args[0] || 'do';
    const help = await module.parseCLI('@help', [topic, '--output', outputMode]);
    stdout.write(help.help);
    return 0;
  }
  if (inv.name === 'cache') return runCache(inv);
  if (inv.name === 'clean') {
    // Node's path-based fs APIs cannot bind ownership validation and removal
    // to a captured object or participate in the native descriptor project lock.
    // Never substitute lstat/hash/unlink, even when the ledger appears empty.
    const result = { schema: 1, type: 'clean-result', entries: [{ path: '', producer: '', state: 'error', reason: 'unsafe-or-unsupported-host-operation' }], removed: 0, preserved: 0, missing: 0, wouldRemove: 0, status: 1 };
    if (jsonMode) stdout.write(`${JSON.stringify(result)}\n`);
    else stderr.write('error clean: descriptor-relative output ownership is unavailable on this host\n');
    return 1;
  }
  if (inv.name === 'run' || inv.name === 'render') return runSession(module, inv, sourceDirectory);
  if (inv.name === 'parse') return runParse(module, inv);
  if (inv.name === 'fmt') return runFmt(module, inv);
  if (inv.name === 'plan') return runPlan(module, inv, sourceDirectory);
  if (inv.name === 'list') return runList(module, inv, sourceDirectory);
  if (inv.name === 'inputs' || inv.name === 'outputs' || inv.name === 'span') return runGraph(module, inv, sourceDirectory);
  if (inv.name === 'tools') return runTools(module, inv, sourceDirectory);
  if (inv.name === 'cat') {
    if (inv.dryRun) return featureUnsupported('--dry-run');
    return runCat(module, inv, sourceDirectory);
  }
  if (inv.dryRun) {
    const input = { kind: inv.command ? 'command' : inv.file === '-' ? 'stdin' : inv.file ? 'file' : 'discover', value: inv.command || inv.file || '', lang: 'kmk', entries: inv.targets };
    return runSession(module, { ...inv, name: 'run', inputs: [input] }, sourceDirectory);
  }
  return runPrimary(module, inv, noArguments === true, sourceDirectory);
}

function runCache(inv) {
  const action = inv.args?.[0];
  if (action !== 'list' && action !== 'clean') throw Object.assign(new Error('cache requires the list or clean action'), { code: 'OPT_VALUE_INVALID' });
  const buckets = [['tasks', '.kame/cache/tasks'], ['host', '.kame/cache/host'], ['file-context', '.kame/cache/file-context']];
  if (action === 'list') {
    const records = [];
    for (const [backend, relative] of buckets) {
      const directory = join(process.cwd(), relative);
      let names;
      try { names = readdirSync(directory).sort(); }
      catch (error) { if (error.code === 'ENOENT') continue; throw Object.assign(new Error(`cannot inspect cache directory: ${error.message}`), { code: 'FS_ERR' }); }
      for (const key of names) {
        try {
          const info = lstatSync(join(directory, key));
          if (info.isFile()) records.push({ backend, key, bytes: info.size });
        } catch (error) {
          if (error.code !== 'ENOENT') throw Object.assign(new Error(`cannot inspect cache record: ${error.message}`), { code: 'FS_ERR' });
        }
      }
    }
  writeReport('cache list', records);
    return 0;
  }
  let removed = 0;
  let wouldRemove = 0;
  for (const [, relative] of buckets) {
    const directory = join(process.cwd(), relative);
    let names;
    try { names = readdirSync(directory).sort(); }
    catch (error) { if (error.code === 'ENOENT') continue; throw Object.assign(new Error(`cannot inspect cache directory: ${error.message}`), { code: 'FS_ERR' }); }
    for (const key of names) {
      try {
        if (lstatSync(join(directory, key)).isFile()) { if (inv.dryRun) wouldRemove++; else { unlinkSync(join(directory, key)); removed++; } }
      } catch (error) {
        if (error.code !== 'ENOENT') throw Object.assign(new Error(`cannot remove cache record: ${error.message}`), { code: 'FS_ERR' });
      }
    }
  }
  if (jsonMode) stdout.write(`${JSON.stringify({ schema: 1, type: 'cache-clean-result', removed, ...(inv.dryRun ? { dryRun: true, wouldRemove } : {}) })}\n`);
  else stderr.write(`${styled('status.success', 'done')} cache clean · ${inv.dryRun ? `would remove ${wouldRemove}` : `removed ${removed}`} cache records\n`);
  return 0;
}

function writeVersion(asJSON) {
  if (asJSON) stdout.write(`${JSON.stringify({ schema: 1, type: 'version', version: version(), buildID: 'unknown', buildTime: 'unknown', buildMode: 'wasm', runtime: 'WASM', ...versionIdentity() })}\n`);
  else stdout.write(`kame ${version()} (runtime=WASM)\n`);
}

async function main() {
  let args = argv.slice(2);
  const first = args[0];

  // Keep simple version requests usable before the WASM module is provisioned.
  if ((args.length === 1 || (args.length === 2 && args.includes('--json'))) &&
      args.some((arg) => arg === '-V' || arg === '--version')) {
    writeVersion(args.includes('--json'));
    return 0;
  }

  if (first === '--wasm-abi-info') {
    stdout.write(`${JSON.stringify((await Module.load()).abiInfo())}\n`);
    return 0;
  }
  if (first === '--wasm-self-test') {
    stdout.write(`${JSON.stringify(await (await Module.load()).selfTest())}\n`);
    return 0;
  }

  installSignals();
  const module = await Module.load();
  const presentation = await module.parseCLI('@presentation', args);
  configurePresentation(presentation);
  if (!presentation.ok) { diagnostic(presentation.error.code, presentation.error.message); return 2; }
  args = presentation.args;
  if (presentation.earlyAction === 'version') {
    writeVersion(jsonMode);
    return 0;
  }
  const helpData = await module.parseCLI('@help', ['--output=json']);
  const commands = JSON.parse(helpData.help).commands.map((command) => command.name);
  const showHelp = async (topic) => {
    if (topic && topic !== 'do' && !commands.includes(topic)) { diagnostic('CMD_UNKNOWN', `unknown command: ${topic}`); return 2; }
    const help = await module.parseCLI('@help', [topic || '', '--output', outputMode]);
    if (jsonMode) stdout.write(help.help);
    else {
      const lines = help.help.split('\n');
      stdout.write(lines.map((line, i) => styled(i === 0 || line.endsWith(':') ? 'heading' : 'text.primary', line, outputColor)).join('\n'));
    }
    return 0;
  };
  if (presentation.earlyAction === 'help') return showHelp(presentation.helpTopic && presentation.helpTopic !== 'do' && !commands.includes(presentation.helpTopic) ? 'do' : presentation.helpTopic);
  if (args[0] === 'do') {
    const command = args[1];
    if (command === undefined) {
      return showHelp('do');
    }
    if (command === 'help') {
      const inv = await module.parseCLI('help', args.slice(2));
      if (!inv.ok) { diagnostic(inv.error.code, inv.error.message); return 2; }
      return showHelp(inv.args[0] || 'do');
    }
    if (!commands.includes(command)) {
      const inv = await module.parseCLI(command, args.slice(2)); diagnostic(inv.error.code || 'CMD_UNKNOWN', inv.error.message || `unknown command: ${command}`); return 2;
    }
    const inv = { ...await module.parseCLI(command, args.slice(2)), output: presentation.output, json: presentation.json, color: presentation.color, diagnosticFormat: presentation.diagnosticFormat };
    if (!inv.ok) {
      diagnostic(inv.error.code, inv.error.message);
      return 2;
    }
    return dispatch(module, inv);
  }
  const inv = { ...await module.parseCLI('', args), output: presentation.output, json: presentation.json, color: presentation.color, diagnosticFormat: presentation.diagnosticFormat };
  if (!inv.ok) {
    diagnostic(inv.error.code, inv.error.message);
    return 2;
  }
  return dispatch(module, inv, args.length === 0);
}

if (process.argv[1] && existsSync(process.argv[1]) && import.meta.url === pathToFileURL(realpathSync(process.argv[1])).href) {
  main().then(
    (status) => { process.exitCode = status; },
    (error) => {
      if (interruptedStatus) { process.exitCode = interruptedStatus; return; }
      if (jsonMode) {
        if (error.diagnostics) {
          for (const detail of error.diagnostics) stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
        } else {
          if (lastDiagnostic === null && error.span !== undefined && primarySource) lastDiagnostic = { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message, source: primarySource.name, span: error.span };
          diagnostic(error.code ?? 'HOST_FAIL', error.message);
        }
      } else {
        if (error.diagnostics) {
          for (const detail of error.diagnostics) stderr.write(renderDiagnostic(detail, primarySource, 80));
          process.exitCode = 1;
          return;
        }
        const detail = error.diagnostics?.[0] ?? lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
        if (!error.diagnostics && !lastDiagnostic && error.span !== undefined && primarySource) { detail.source = primarySource.name; detail.span = error.span; }
        stderr.write(renderDiagnostic(detail, primarySource, 80));
        if (buildProgress !== null && buildProgress.completed + buildProgress.failed + buildProgress.cancelled !== 0) printSummary();
      }
      process.exitCode = 1;
    },
  );
}
