#!/usr/bin/env node
// Kame JavaScript CLI: the WebAssembly counterpart of the native CLI in
// docs/spec/009-cli.md. One ESM file, Node 18+, no third-party dependencies,
// an adjacent kame.wasm. See docs/spec/010-wasm.md.
//
// The command grammar (options, defaults, arity, conflicts, usage diagnostics)
// is parsed by the shared portable `cli` package inside the module via
// kame_wasm_cli, so the native CLI and this wrapper accept the same words. This
// file supplies only host capabilities and stream/exit policy.
import { mkdir, mkdtemp, open, readFile, rename, rm, stat } from 'node:fs/promises';
import { accessSync, closeSync, constants as fsConstants, existsSync, mkdtempSync, openSync, readFileSync, readSync, readdirSync, rmSync, rmdirSync, statSync, unlinkSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, isAbsolute, join, normalize, resolve } from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { constants as osConstants, tmpdir } from 'node:os';
import process, { argv, env, stderr, stdout } from 'node:process';

// Children run in their own process group so a signal terminates the whole
// tree, and a forced termination reports 128 plus the signal number.
const activeChildren = new Set();
const invocationCancellations = new Set();
let interruptedStatus = 0;

function installSignals() {
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
      if (interruptedStatus) return;
      interruptedStatus = 128 + (osConstants.signals[signal] ?? 0);
      const reaped = [...activeChildren].map((child) => new Promise((resolve) => child.once('close', resolve)));
      for (const cancellation of invocationCancellations) cancellation.abort();
      for (const child of activeChildren) {
        try {
          process.kill(-child.pid, 'SIGKILL');
        } catch {
          try {
            child.kill('SIGKILL');
          } catch {
            // already gone
          }
        }
      }
      Promise.all(reaped).then(() => process.exit(interruptedStatus));
    });
  }
}

function track(child) {
  activeChildren.add(child);
  child.once('close', () => activeChildren.delete(child));
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

let jsonMode = false;
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
  if (jsonMode) {
    const detail = lastDiagnostic ?? { code, severity: 'error', message };
    stdout.write(`${JSON.stringify({ schema: 1, type: 'diagnostic', diagnostic: detail })}\n`);
    return;
  }
  stderr.write(`error ${code}: ${message}\n`);
}

// resolveColor mirrors the native CLI: color only applies to the human format
// and respects NO_COLOR, CLICOLOR, CLICOLOR_FORCE, TERM, and a TTY check.
function resolveColor(color, format) {
  if (format !== 'human' || color === 'never') return false;
  if (color === 'always') return true;
  if (env.NO_COLOR || env.CLICOLOR === '0' || env.TERM === 'dumb') return false;
  if (env.CLICOLOR_FORCE && env.CLICOLOR_FORCE !== '0') return true;
  return stderr.isTTY === true;
}

// cacheEntryPath stores one opaque host cache record under the project cache
// root, keyed by the runtime's opaque byte key.
function cacheEntryPath(key) {
  return join(process.cwd(), '.kame', 'cache', 'host', Buffer.from(key).toString('hex'));
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
  if (diagnosticColor) out += d.severity === 'warning' ? '\x1b[33m' : '\x1b[31m';
  if (d.target) {
    if (diagnosticFormat === 'human') out += `✗ ${d.target} failed · ${d.code}\n`;
    else out += `${d.target} failed: ${d.code}\n`;
    if (d.targetStack && d.targetStack.length > 1) {
      out += `  required by ${d.targetStack.join(diagnosticFormat === 'human' ? ' → ' : ' -> ')}\n`;
    }
  }
  const span = d.span ?? { start: 0, end: 0 };
  if (d.source && renderSource && d.source === renderSource.name) {
    const sourceStart = sourceIndex(renderSource.text, span.start);
    const sourceEnd = sourceIndex(renderSource.text, span.end);
    const pos = sourcePosition(renderSource.text, sourceStart);
    out += `${d.source}:${pos.line}:${pos.column}: ${severityName(d.severity)} ${d.code}: ${d.message}\n`;
    let start = sourceStart;
    if (start < 0) start = 0;
    if (start > renderSource.text.length) start = renderSource.text.length;
    const [lineStart, lineEnd] = lineBounds(renderSource.text, start);
    out += wrappedExcerpt(renderSource.text, lineStart, lineEnd, start, sourceEnd, width);
  } else if (d.source) {
    out += `${d.source}: ${severityName(d.severity)} ${d.code}: ${d.message}\n`;
  } else {
    out += `${severityName(d.severity)} ${d.code}: ${d.message}\n`;
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
  if (diagnosticColor) out += '\x1b[0m';
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
  if (event.type === 'stdout') { stdout.write(eventData(event)); return; }
  if (event.type === 'stderr') { stderr.write(eventData(event)); return; }
  if (event.type === 'target-started') {
    buildProgress.active++;
    stderr.write(`[${event.target}] started (${buildProgress.active} active, ${buildProgress.completed} complete)\n`);
    return;
  }
  if (event.type === 'target-completed') {
    if (buildProgress.active !== 0) buildProgress.active--;
    buildProgress.completed++;
    stderr.write(`[${event.target}] complete (${buildProgress.active} active, ${buildProgress.completed} complete)\n`);
    return;
  }
  if (event.type === 'target-failed' || event.type === 'target-cancelled') {
    if (buildProgress.active !== 0) buildProgress.active--;
    buildProgress.failed++;
    stderr.write(`[${event.target}] failed (${buildProgress.active} active, ${buildProgress.completed} complete)\n`);
    if (event.diagnostic) lastDiagnostic = event.diagnostic;
    return;
  }
  if (event.type === 'cache-warning' && event.diagnostic) {
    stderr.write(`warning ${event.diagnostic.code}: ${event.diagnostic.message}\n`);
  }
}

function eventData(event) {
  if (event.data === undefined) return '';
  if (event.encoding === 'base64') return Buffer.from(event.data, 'base64');
  return event.data;
}

function formatSeconds(elapsedMS) {
  return `${Math.floor(elapsedMS / 1000)}.${String(elapsedMS % 1000).padStart(3, '0')}s`;
}

function printSummary() {
  const elapsedMS = Date.now() - buildStartedAt;
  if (buildProgress.failed === 0) {
    stderr.write(`Summary: ${buildProgress.completed} ${buildProgress.completed === 1 ? 'target' : 'targets'} complete in ${formatSeconds(elapsedMS)}\n`);
  } else {
    stderr.write(`Summary: ${buildProgress.completed} complete, ${buildProgress.failed} failed in ${formatSeconds(elapsedMS)}\n`);
  }
}

function diagnosticError(text, fallbackCode) {
  if (typeof text === 'string' && text.length !== 0) {
    const at = text.indexOf(': ');
    if (at > 0) return Object.assign(new Error(text.slice(at + 2)), { code: text.slice(0, at) });
    return Object.assign(new Error(text), { code: fallbackCode });
  }
  return Object.assign(new Error(fallbackCode), { code: fallbackCode });
}

function usage() {
  stdout.write('kame - a modern build system in the spirit of GNU Make.\n\n');
  stdout.write('Usage:\n');
  stdout.write('  kame [OPTIONS] [TARGET...]\n');
  stdout.write('  kame do COMMAND [OPTIONS] [ARG...]\n\n');
  stdout.write('Commands:\n');
  stdout.write('  do run          execute ordered source fragments in one session\n');
  stdout.write('  do plan         print the resolved plan as JSON\n');
  stdout.write('  do inputs       list declared input paths (--depth N)\n');
  stdout.write('  do outputs      list declared output paths (--depth N)\n');
  stdout.write('  do span         show transitive inputs and outputs (--expand, --depth N)\n');
  stdout.write('  do tools        list globally referenced build tools\n');
  stdout.write('  do parse        parse a language file and print a JSON AST\n');
  stdout.write('  do fmt          format source in place (-i) or check it (-n)\n');
  stdout.write('  do render       render a document template (--define, --comment, --check)\n');
  stdout.write('  do cat TARGET   materialize one target and print its artifact\n');
  stdout.write('  do help         show this help\n\n');
  stdout.write('Options: -f FILE  -c TEXT  -C DIR  -j N  -n  --force  -h  -V\n');
  stdout.write('Later-stage behavior reports FEATURE_UNSUP.\n');
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
    if (pi < pattern.length && pattern[pi] === '*') {
      star = pi++;
      mark = ni;
      continue;
    }
    if (pi < pattern.length && (pattern[pi] === '?' || pattern[pi] === name[ni])) {
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
    if (pattern[i] === '\\' && i + 1 < pattern.length) {
      i++;
      if (pattern[i] === ch) matched = true;
      i++;
      continue;
    }
    if (i + 2 < pattern.length && pattern[i + 1] === '-' && pattern[i + 2] !== ']') {
      if (ch >= pattern[i] && ch <= pattern[i + 2]) matched = true;
      i += 3;
      continue;
    }
    if (pattern[i] === ch) matched = true;
    i++;
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
    if (pattern[i] === '*' || pattern[i] === '?' || pattern[i] === '[') { cut = i; break; }
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
  } catch {
    return;
  }
  for (const entry of entries) {
    const name = join(directory, entry.name);
    names.push(name);
    if (entry.isDirectory()) collectPaths(name, names);
  }
}

function wildcardPaths(pattern) {
  while (pattern.startsWith('./')) pattern = pattern.slice(2);
  const names = [];
  collectPaths(globRoot(pattern), names);
  const matches = [];
  for (const name of names) {
    if (matchSegments(pattern, 0, name, 0)) matches.push(name.startsWith('/') ? name : `./${name}`);
  }
  matches.sort();
  return matches;
}

async function writeFileAtomic(name, data, durable = false) {
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

function resolveTool(name) {
  if (name === '') return '';
  if (name.startsWith('/')) return isExecutable(name) ? name : '';
  const pathValue = env.PATH ?? '';
  for (const directory of pathValue.split(':')) {
    const candidate = resolve(process.cwd(), directory, name);
    if (isExecutable(candidate)) return candidate;
  }
  return '';
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
  'kame_wasm_set_source_name',
  'kame_wasm_parse',
  'kame_wasm_format',
  'kame_wasm_cli',
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
  'kame_wasm_process_stream',
  'kame_wasm_process_terminal',
  'kame_wasm_instance_diagnostic_length',
  'kame_wasm_instance_diagnostic_copy',
  'kame_wasm_instance_diagnostic_span',
];

class Module {
  constructor(exports) {
    this.exports = exports;
    this.path = wasmPath;
  }

  static async load() {
    let bytes;
    try {
      bytes = await readFile(wasmPath);
    } catch (error) {
      throw Object.assign(new Error(`cannot read ${wasmPath}: ${error.message}`), { code: 'FS_ERR' });
    }
    const { instance } = await WebAssembly.instantiate(bytes, {});
    const exports = instance.exports;
    const missing = REQUIRED_EXPORTS.filter((name) => !(name in exports));
    if (missing.length !== 0) throw Object.assign(new Error(`WASM ABI is incomplete: missing ${missing.join(', ')}`), { code: 'FEATURE_UNSUP' });
    if (exports.kame_wasm_abi_version() !== 1) throw Object.assign(new Error('unsupported WASM ABI schema'), { code: 'FEATURE_UNSUP' });
    return new Module(exports);
  }

  abiInfo() {
    return { schema: this.exports.kame_wasm_abi_version(), stage: STAGE, wasm: this.path, exports: Object.keys(this.exports).sort() };
  }

  allocate(size, alignment = 1) {
    const pointer = this.exports.kame_wasm_alloc(size, alignment);
    if (pointer === 0) throw new Error(`WASM allocation failed for ${size} bytes`);
    return pointer;
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
    for (;;) {
      const query = call(instance, 0, 0, lengthPointer);
      if (query !== 6) return this.copyQuery(call, instance);
      if (this.exports.kame_wasm_step(instance) !== 1) throw Object.assign(new Error('inspection host request unavailable'), { code: 'HOST_FAIL' });
      if (await this.service(instance, context) !== 0) throw Object.assign(new Error('inspection host completion failed'), { code: 'HOST_FAIL' });
    }
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
    let data = null;
    if (kind === 2) {
      const dataLength = this.exports.kame_wasm_next_request_data_length(instance);
      const dataPointer = this.allocate(dataLength || 1);
      if (this.exports.kame_wasm_request_data_copy(instance, dataPointer, dataLength) !== 0) throw Object.assign(new Error('request data copy failed'), { code: 'HOST_FAIL' });
      data = new Uint8Array(this.exports.memory.buffer, dataPointer, dataLength).slice();
    }
    let key = null;
    let record = null;
    if (kind === 10 || kind === 11 || kind === 12) {
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
    return this.dispatch(instance, request, kind, payload, data, context, key, record);
  }

  async dispatch(instance, request, kind, payload, data, context, key, record) {
    const grants = context.grants;
    if (kind === 1) {
      if (!grants.read) return this.deny(instance, request);
      try {
        const bytes = await readFile(payload);
        const pointer = this.allocate(bytes.length || 1);
        new Uint8Array(this.exports.memory.buffer, pointer, bytes.length).set(bytes);
        return this.exports.kame_wasm_complete_bytes(instance, request, pointer, bytes.length);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', context.inspection ? 'cannot read file' : `cannot read file: ${error.message}`);
      }
    }
    if (kind === 5) {
      if (!grants.read) return this.deny(instance, request);
      try {
        const info = await stat(payload);
        return this.completeJSON(instance, request, { name: payload, size: info.size, mode: info.mode, dir: info.isDirectory() });
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', context.inspection ? 'cannot stat file' : `cannot stat file: ${error.message}`);
      }
    }
    if (kind === 7 || kind === 17) {
      if (kind === 7 && !grants.read) return this.deny(instance, request);
      return this.completeJSON(instance, request, existsSync(payload));
    }
    if (kind === 6) {
      if (!grants.read) return this.deny(instance, request);
      return this.completeJSON(instance, request, wildcardPaths(payload));
    }
    if (kind === 2) {
      if (!grants.write) return this.deny(instance, request);
      try {
        await writeFileAtomic(payload, data);
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
    if (kind === 13 || kind === 14 || kind === 15) {
      if (!grants.run) return this.deny(instance, request);
      const decoded = JSON.parse(payload);
      const stages = kind === 13 ? [decoded] : kind === 14 ? decoded : decoded.stages;
      const redirections = kind === 15 ? decoded : { input: '', output: '', append: false };
      for (const [field, capability] of [['input', 'read'], ['output', 'write']]) {
        const name = redirections[field];
        if (typeof name !== 'string' || name.includes('\0')) return this.completeFailure(instance, request, 'EXPR_INVALID', 'invalid redirection path');
        if (name && (!grants[capability] || !pathGranted(resolve(name), context[`${capability}Roots`]))) return this.deny(instance, request);
      }
      if (context.runRoots !== null && context.runRoots !== undefined) {
        for (const args of stages) {
          if (!args[0].includes('/')) return this.deny(instance, request);
          const executable = resolve(args[0]);
          if (!context.runRoots.some((root) => executable === root || executable.startsWith(`${root}/`))) return this.deny(instance, request);
        }
      }
      const detached = context.concurrent === true;
      const completion = await runArgvCapture(stages, { ...context, ...redirections, onStdout: redirections.stream ? (chunk) => this.processStream(instance, false, chunk, detached ? request : undefined) : null, onStderr: context.streaming ? (chunk) => this.processStream(instance, true, chunk, detached ? request : undefined) : null });
      if (completion.ok) {
        if (!redirections.stream && completion.value.status === 0 && completion.value.signal === 0) {
          const encoded = this.write(completion.value.stdout);
          return this.exports.kame_wasm_complete_text(instance, request, encoded.pointer, encoded.length);
        }
        return this.completeJSON(instance, request, completion.value);
      }
      return this.completeFailure(instance, request, completion.code, completion.message);
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
      }
      if (context.streaming) {
        const detached = context.concurrent === true;
        await runProcess(this, instance, payload, context, detached ? request : undefined);
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
      try {
        const path = cacheEntryPath(key);
        await writeFileAtomic(path, record, true);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot write cache record: ${error.message}`);
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

  deny(instance, request) {
    return this.completeFailure(instance, request, 'CAP_DENIED', 'operation capability denied');
  }

  async evaluate(source, expression, context) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const compiled = this.write(source);
      if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      const encoded = this.write(expression);
      if (this.exports.kame_wasm_expression_begin(instance, encoded.pointer, encoded.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'PARSE_ERR');
      for (;;) {
        const state = this.exports.kame_wasm_step(instance);
        this.drainExpressionEffects(instance);
        if (state === 2) break;
        if (state !== 1) continue;
        await this.service(instance, context);
      }
      return this.copyResult(instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
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

  async prepared(source, run, name, context) {
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
      if (context) return await this.copyInspectionQuery(run(instance), instance, context);
      return this.copyQuery(run(instance), instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  compileBuild(instance, source, name) {
    if (name) this.setSourceName(instance, name);
    const compiled = this.write(Array.isArray(source) ? '' : source);
    if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
    if (Array.isArray(source)) {
      if (!this.exports.kame_wasm_set_build_sources) throw Object.assign(new Error('include source ABI unavailable'), { code: 'FEATURE_UNSUP' });
      const descriptor = this.write(JSON.stringify({ sources: source }));
      if (this.exports.kame_wasm_set_build_sources(instance, descriptor.pointer, descriptor.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
    }
  }

  setSourceName(instance, name) {
    const bytes = this.write(name);
    if (this.exports.kame_wasm_set_source_name(instance, bytes.pointer, bytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
  }

  async planJSON(source, target, expand, name) {
    return this.prepared(source, (instance) => {
      const targetBytes = this.write(target);
      const flag = expand ? 1 : 0;
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_plan(handle, targetBytes.pointer, targetBytes.length, flag, dst, dstLen, lengthPointer);
    }, name);
  }

  async graphJSON(source, target, depth, kind, expand, name, context) {
    return this.prepared(source, (instance) => {
      const targetBytes = this.write(target);
      const flag = expand ? 1 : 0;
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_graph(handle, targetBytes.pointer, targetBytes.length, depth, kind, flag, dst, dstLen, lengthPointer);
    }, name, context);
  }

  async toolNames(source, name) {
    const bytes = await this.prepared(source, () => {
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_tools(handle, dst, dstLen, lengthPointer);
    }, name);
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
    const names = await this.toolNames(source, name);
    return this.prepared(source, (instance) => {
      for (const tool of names) this.setToolPath(instance, tool, resolveTool(tool));
      const t = this.write(target);
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_tools_check(handle, t.pointer, t.length, dst, dstLen, lengthPointer);
    }, name, context);
  }

  // drainEvents pops every queued target event. JSON mode writes the raw event
  // lines to stdout; a human primary run forwards process streams and progress
  // to stdout/stderr like the native CLI.
  drainEvents(instance, context) {
    const json = context.json === true;
    const human = context.human === true && !json;
    for (;;) {
      const lengthPointer = this.allocate(4, 4);
      const query = this.exports.kame_wasm_target_event(instance, 0, 0, lengthPointer);
      if (query !== 0 && query !== 3) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      const length = new DataView(this.exports.memory.buffer, lengthPointer, 4).getUint32(0, true);
      if (length === 0) return;
      const output = this.allocate(length || 1);
      if (this.exports.kame_wasm_target_event(instance, output, length, lengthPointer) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      const bytes = new Uint8Array(this.exports.memory.buffer, output, length).slice();
      for (const line of this.decode0(bytes).trim().split('\n')) {
        const event = JSON.parse(line);
        if (event.diagnostic && (event.type === 'target-failed' || event.type === 'target-cancelled')) lastDiagnostic = event.diagnostic;
      }
      if (json) {
        stdout.write(bytes);
      } else if (human) {
        humanEvent(bytes);
      }
    }
  }

  processStarted(instance, request) {
    const status = request === undefined ? this.exports.kame_wasm_process_started(instance) : this.exports.kame_wasm_process_started_request(instance, request);
    if (status !== 0) throw new Error('process start event failed');
  }

  processStream(instance, stderrStream, chunk, request) {
    const pointer = this.allocate(chunk.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, chunk.length).set(chunk);
    const status = request === undefined
      ? this.exports.kame_wasm_process_stream(instance, stderrStream ? 1 : 0, pointer, chunk.length)
      : this.exports.kame_wasm_process_stream_request(instance, request, stderrStream ? 1 : 0, pointer, chunk.length);
    if (status !== 0) throw new Error('process stream event failed');
  }

  processTerminal(instance, outcome, status, signal, stdoutBytes, stderrBytes, code, message, request) {
    if (request !== undefined && this.exports.kame_wasm_request_attach(instance, request) !== 0) throw new Error('process completion correlation failed');
    const stdout = this.write(stdoutBytes ?? new Uint8Array(0));
    const stderr = this.write(stderrBytes ?? new Uint8Array(0));
    const codeBytes = this.write(code);
    const messageBytes = this.write(message);
    if (this.exports.kame_wasm_process_terminal(instance, status, signal, outcome, stdout.pointer, stdout.length, stderr.pointer, stderr.length, codeBytes.pointer, codeBytes.length, messageBytes.pointer, messageBytes.length) !== 0) throw new Error('process terminal event failed');
  }

  async materialize(source, target, context, name) {
    const tools = await this.toolNames(source, name);
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      this.compileBuild(instance, source, name);
      for (const tool of tools) this.setToolPath(instance, tool, resolveTool(tool));
      const directoryBytes = this.write(process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directoryBytes.pointer, directoryBytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      if (this.exports.kame_wasm_set_forwarding(instance, 1) !== 0) throw Object.assign(new Error('request forwarding is unavailable'), { code: 'FEATURE_UNSUP' });
      context.streaming = true;
      const encoded = this.write(target);
      if (this.exports.kame_wasm_target_begin(instance, encoded.pointer, encoded.length) !== 0) throw this.compileFailure(instance, 'TGT_NO_RULE');
      for (;;) {
        const state = this.exports.kame_wasm_step(instance);
        this.drainEvents(instance, context);
        if (state === 2) break;
        if (state !== 1) continue;
        await this.service(instance, context);
      }
      const kind = this.exports.kame_wasm_result_kind(instance);
      return { kind, bytes: this.copyResult(instance) };
    } finally {
      this.exports.kame_wasm_instance_free(instance);
    }
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
    const cancellation = new AbortController();
    invocationCancellations.add(cancellation);
    context.concurrent = true;
    context.signal = cancellation.signal;
    let hostError;
    try {
      if (this.exports.kame_wasm_source_compile(instance, 0, 0) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      const directory = this.write(process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directory.pointer, directory.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      this.inspectionGrant(instance, '', '');
      const grants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }];
      for (const grant of grants) {
        if (grant.names.length === 0) this.inspectionGrant(instance, grant.capability, '');
        else for (const name of grant.names) this.inspectionGrant(instance, grant.capability, name);
      }
      const descriptor = this.write(JSON.stringify({ fragments, args: inv.args, captureLimit: inv.captureLimit, json: inv.json ? 1 : 0, dryRun: inv.dryRun ? 1 : 0 }));
      if (this.exports.kame_wasm_session_compile(instance, descriptor.pointer, descriptor.length) !== 0) {
        if (inv.json) { this.drainEvents(instance, context); return 1; }
        throw this.compileFailure(instance, 'PARSE_ERR');
      }
      context.streaming = true;
      context.human = !inv.json;
      let last;
      const deadline = inv.timeoutMS > 0 ? performance.now() + inv.timeoutMS : Infinity;
      const count = this.exports.kame_wasm_session_work_count(instance);
      for (let i = 0; i < count; i++) {
        const kind = this.exports.kame_wasm_session_work_kind(instance, i);
        if (this.exports.kame_wasm_session_work_begin(instance, i) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
        for (;;) {
          context.timeoutMS = Number.isFinite(deadline) ? Math.max(1, Math.ceil(deadline - performance.now())) : 0;
          if (performance.now() >= deadline) throw Object.assign(new Error('invocation timed out'), { code: 'RECIPE_TIMEOUT' });
          const state = this.exports.kame_wasm_step(instance);
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
      if (last !== undefined) stdout.write(last);
      return 0;
    } catch (error) {
      if (inv.json && lastDiagnostic !== null) return 1;
      throw error;
    } finally {
      cancellation.abort();
      await Promise.all(pending);
      invocationCancellations.delete(cancellation);
      this.exports.kame_wasm_instance_free(instance);
    }
  }

  async selfTest() {
    const info = this.abiInfo();
    const context = { grants: { read: true, write: true, run: true, env: true } };
    const pure = this.decode0(await this.evaluate('', '(join ["a" "b"] ":")', context));
    const expression = this.decode0(await this.evaluate('', '(count [1 2 3])', context));
    return { ...info, selfTest: { pure, expression } };
  }

  decode0(bytes) {
    return new TextDecoder().decode(bytes);
  }
}

// runProcessDirect services a process request without an event stream; used by
// expression evaluation, which has no target program to emit events for.
function runProcessDirect(script, context) {
  return new Promise((resolveRun) => {
    const shell = context.shell.length !== 0 ? context.shell : ['/bin/sh', '-c'];
    const childEnv = { ...process.env };
    for (const entry of context.environment) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const child = spawn(shell[0], [...shell.slice(1), script], { stdio: ['ignore', 'pipe', 'pipe'], env: childEnv, detached: true });
    track(child);
    const out = [];
    let failure = null;
    child.stdout.on('data', (chunk) => out.push(chunk));
    child.stderr.on('data', () => {});
    child.once('error', (error) => { failure = error; });
    child.once('close', (status) => {
      if (failure !== null) resolveRun({ ok: false, code: 'HOST_FAIL', message: failure.message });
      else if (status !== 0) resolveRun({ ok: false, code: 'RECIPE_FAIL', message: `process exited with status ${status}` });
      else resolveRun({ ok: true, text: Buffer.concat(out).toString('utf8') });
    });
  });
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

function runArgvCapture(stages, context) {
  if (context.signal?.aborted) return Promise.resolve({ ok: false, code: 'EXEC_CANCELLED', message: 'invocation cancelled' });
  if (!Array.isArray(stages) || stages.length === 0 || stages.some((args) => !Array.isArray(args) || args.length === 0 || !args[0] || args.some((arg) => typeof arg !== 'string' || arg.includes('\0')))) {
    return Promise.resolve({ ok: false, code: 'EXPR_INVALID', message: 'invalid process argv' });
  }
  const setups = context.setup ?? [];
  if (!Array.isArray(setups) || (setups.length !== 0 && setups.length !== stages.length) || setups.some((setup) => typeof setup.cwd !== 'string' || setup.cwd.includes('\0') || !Number.isSafeInteger(setup.timeoutMS) || setup.timeoutMS < 0 || !Array.isArray(setup.environment) || setup.environment.some((entry) => typeof entry !== 'string' || entry.includes('\0') || !/^[A-Za-z_][A-Za-z0-9_]*=/.test(entry)))) {
    return Promise.resolve({ ok: false, code: 'EXPR_INVALID', message: 'invalid stage setup' });
  }
  return new Promise((resolveRun) => {
    const childEnv = { ...process.env };
    for (const entry of context.environment ?? []) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const configurations = stages.map((_, i) => {
      const setup = setups[i];
      const env = { ...childEnv };
      for (const entry of setup?.environment ?? []) { const at = entry.indexOf('='); env[entry.slice(0, at)] = entry.slice(at + 1); }
      return { env, cwd: setup?.cwd || process.cwd(), timeoutMS: setup?.timeoutMS ?? 0 };
    });
    let executables;
    try {
      for (const configuration of configurations) if (!statSync(configuration.cwd).isDirectory()) throw new Error('invalid stage cwd');
      executables = stages.map((args, i) => argvExecutable(args[0], configurations[i].env, configurations[i].cwd));
    }
    catch { resolveRun({ ok: false, code: 'HOST_FAIL', message: 'cannot start process' }); return; }
    let pipes;
    try { pipes = pipelinePipes(stages.length - 1); }
    catch { resolveRun({ ok: false, code: 'HOST_FAIL', message: 'cannot create pipeline pipes (POSIX mkfifo required)' }); return; }
    let input, output;
    try {
      // Input opens first: missing sources must not truncate destinations.
      if (context.input) input = openSync(context.input, 'r');
      if (context.output) output = openSync(context.output, context.append ? 'a' : 'w');
    } catch {
      if (input !== undefined) closeSync(input);
      for (const pipe of pipes) { closeSync(pipe.read); closeSync(pipe.write); }
      resolveRun({ ok: false, code: 'HOST_FAIL', message: 'cannot open process redirection' });
      return;
    }
    const children = [];
    const results = [];
    const chunks = [];
    const limit = context.captureLimit > 0 ? context.captureLimit : 1024 * 1024;
    let length = 0;
    let failure = null;
    const timers = [];
    let remaining = 0;
    let launched = false;
    const stop = (code, message) => {
      if (failure !== null) return;
      failure = { ok: false, code, message };
      for (const child of children) {
        if (child.pid === undefined) continue;
        try { process.kill(-child.pid, 'SIGKILL'); } catch { child.kill('SIGKILL'); }
      }
    };
    const cancelled = () => stop('EXEC_CANCELLED', 'invocation cancelled');
    context.signal?.addEventListener('abort', cancelled, { once: true });
    if (context.timeoutMS > 0) timers.push(processDeadline(context.timeoutMS, () => stop('RECIPE_TIMEOUT', 'process timed out')));
    const finish = () => {
      if (!launched || remaining !== 0) return;
      for (const cancel of timers) cancel();
      context.signal?.removeEventListener('abort', cancelled);
      if (failure !== null) { resolveRun(failure); return; }
      let status = 0, signal = 0;
      for (const result of results) {
        if (result.status !== 0 || result.signal !== 0) { status = result.signal ? 128 + result.signal : result.status; signal = result.signal; }
      }
      // Let the portable evaluator classify exit failure before decoding output.
      if (!context.acceptExit && (status !== 0 || signal !== 0)) { resolveRun({ ok: true, value: { status, signal, stages: results, stdout: '' } }); return; }
      if (context.stream) { resolveRun({ ok: true, value: { status, signal, stages: results, stdout: '' } }); return; }
      let output;
      try { output = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(Buffer.concat(chunks)); }
      catch { resolveRun({ ok: false, code: 'CAPTURE_ENCODING', message: 'command substitution output is not valid UTF-8' }); return; }
      resolveRun({ ok: true, value: { status, signal, stages: results, stdout: output } });
    };
    try {
      for (let i = 0; i < stages.length; i++) {
        const args = stages[i];
        const configuration = configurations[i];
        const child = spawn(executables[i], args.slice(1), { argv0: args[0], shell: false, stdio: [i === 0 ? input ?? 'ignore' : pipes[i - 1].read, i === stages.length - 1 ? output ?? 'pipe' : pipes[i].write, 'pipe'], env: configuration.env, cwd: configuration.cwd, detached: true });
        children.push(child);
        remaining++;
        track(child);
        const cancel = configuration.timeoutMS > 0 ? processDeadline(configuration.timeoutMS, () => stop('RECIPE_TIMEOUT', 'stage timed out')) : () => {};
        timers.push(cancel);
        child.once('exit', cancel);
        child.stderr.on('data', (chunk) => { if (context.onStderr) context.onStderr(chunk); else stderr.write(chunk); });
        child.once('error', () => stop('HOST_FAIL', 'cannot start process'));
        child.once('close', (status, signalName) => {
          results[i] = { status: status ?? 0, signal: osConstants.signals[signalName] ?? 0, outcome: 0 };
          remaining--;
          finish();
        });
        if (i === stages.length - 1 && child.stdout !== null) child.stdout.on('data', (chunk) => {
          if (context.stream) { context.onStdout(chunk); return; }
          length += chunk.length;
          if (length > limit) stop('CAPTURE_LIMIT', 'command substitution exceeded capture limit');
          else if (failure === null) chunks.push(chunk);
        });
      }
    } catch { stop('HOST_FAIL', 'cannot start process'); }
    finally {
      for (const fd of [input, output]) {
        if (fd !== undefined) { try { closeSync(fd); } catch { stop('HOST_FAIL', 'cannot close redirection'); } }
      }
      for (const pipe of pipes) for (const fd of [pipe.read, pipe.write]) {
        try { closeSync(fd); } catch { stop('HOST_FAIL', 'cannot close pipeline pipe'); }
      }
    }
    launched = true;
    finish();
  });
}

// Node's 'pipe' stdio uses socketpairs on Unix; unread bytes on an early close
// can turn SIGPIPE into ECONNRESET/exit 1. Pass real blocking FIFO descriptors to
// the children instead. mkfifo is a POSIX host utility, never a user shell.
function pipelinePipes(count) {
  if (count === 0) return [];
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
async function runProcess(module, instance, script, context, request) {
  let attempt = 0;
  for (;;) {
    const outcome = await runProcessAttempt(module, instance, script, context, request);
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

function runProcessAttempt(module, instance, script, context, request) {
  return new Promise((resolveAttempt) => {
    const shell = context.shell.length !== 0 ? context.shell : ['/bin/sh', '-c'];
    const childEnv = { ...process.env };
    for (const entry of context.environment) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const child = spawn(shell[0], [...shell.slice(1), script], { stdio: ['ignore', 'pipe', 'pipe'], env: childEnv, detached: true });
    track(child);
    module.processStarted(instance, request);
    const out = [];
    const err = [];
    let failure = null;
    let timedOut = false;
    let timer = null;
    if (context.timeoutMS > 0) {
      timer = setTimeout(() => { timedOut = true; child.kill('SIGKILL'); }, context.timeoutMS);
    }
    const cancelled = () => { try { process.kill(-child.pid, 'SIGKILL'); } catch { child.kill('SIGKILL'); } };
    context.signal?.addEventListener('abort', cancelled, { once: true });
    if (context.signal?.aborted) cancelled();
    child.stdout.on('data', (chunk) => { out.push(chunk); module.processStream(instance, false, chunk, request); });
    child.stderr.on('data', (chunk) => { err.push(chunk); module.processStream(instance, true, chunk, request); });
    child.once('error', (error) => { failure = error; });
    child.once('close', (code, signalName) => {
      if (timer !== null) clearTimeout(timer);
      context.signal?.removeEventListener('abort', cancelled);
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
      throw Object.assign(new Error(`cannot change directory: ${error.message}`), { code: 'FS_ERR' });
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
  if (inv.file) {
    try {
      text = await readFile(inv.file, 'utf8');
    } catch (error) {
      return failure('FS_ERR', `cannot read source: ${inv.file}`);
    }
    name = inv.file;
  } else {
    text = await readStdin();
  }
  const bytes = await module.parse(inv.lang, name, text);
  stdout.write(bytes);
  const document = JSON.parse(new TextDecoder().decode(bytes));
  return document.diagnostics && document.diagnostics.some((entry) => entry.severity === 'error') ? 1 : 0;
}

async function runSession(module, inv, sourceDirectory) {
  const fragments = [];
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
    if (fileBacked && (input.lang === 'km' || input.lang === 'kmk')) {
      const parts = await expandSessionIncludes(module, sourceDirectory, name, text, input.lang);
      for (let j = 0; j < parts.length; j++) fragments.push({ ...parts[j], lang: input.lang, entries: j + 1 === parts.length ? input.entries : [], inline: j + 1 === parts.length ? 0 : 1, skipStatements: input.entries.length ? 1 : 0 });
    } else fragments.push({ name, text, lang: input.lang, entries: input.entries, inline: fileBacked ? 0 : 1, comment: inv.comment, defines: inv.defines, check: inv.check ? 1 : 0 });
  }
  if (!inv.dryRun && inv.inputs.length === 1 && inv.inputs[0].lang === 'kmk' && fragments.length === 1) {
    const input = inv.inputs[0];
    if (input.kind !== 'stdin') return runPrimary(module, { ...inv, name: '', sourceName: input.kind === 'command' ? '<command:1>' : fragments[0].name, file: input.kind === 'file' ? resolve(sourceDirectory, fragments[0].name) : '', command: input.kind === 'command' ? input.value : '', targets: input.entries }, false, sourceDirectory);
  }
  buildProgress = { active: 0, completed: 0, failed: 0 };
  return module.runSession(fragments, inv, contextFor(inv));
}

async function discoverBuildSource(module, inv, sourceDirectory) {
  const source = await discoverSource(inv);
  if (source === null) return null;
  const name = inv.sourceName ?? (inv.command ? source.name : isAbsolute(source.name) ? normalize(source.name) : normalize(join(inv.directory || '.', source.name)));
  const parts = await expandSessionIncludes(module, sourceDirectory, name, source.text, 'kmk', [], !inv.command, false);
  // Ordinary sources retain the existing ABI; includes carry source identities.
  return { name, text: source.text, compiled: parts.length === 1 ? source.text : parts };
}

// Syntax and byte spans come from the portable parser, not a second JS grammar.
async function expandSessionIncludes(module, sourceDirectory, name, text, lang, active = [], fileBacked = true, validate = true) {
  const identity = resolve(sourceDirectory, name);
  sourceTexts.set(name, text);
  if (active.includes(identity)) throw Object.assign(new Error(`include cycle: ${name}`), { code: 'DEP_CYCLE' });
  const document = JSON.parse(new TextDecoder().decode(await module.parse(lang === 'kmk' ? 'script' : lang, name, text)));
  const invalid = document.diagnostics.find((item) => item.severity === 'error');
  if (invalid && validate) {
    primarySource = { name, text };
    throw Object.assign(new Error(invalid.message), { code: invalid.code, span: invalid.span, diagnostics: document.diagnostics.filter((item) => item.severity === 'error').map((item) => ({ ...item, source: name })) });
  }
  const bytes = new TextEncoder().encode(text);
  const parts = [];
  let start = 0;
  for (const item of document.ast.items) {
    if (item.kind !== 'include') continue;
    if (!fileBacked) throw Object.assign(new Error('include requires a file-backed build source'), { code: 'FEATURE_UNSUP' });
    parts.push({ name, text: new TextDecoder().decode(bytes.subarray(start, item.span.start)), offset: start });
    const included = normalize(isAbsolute(item.path) ? item.path : join(dirname(name), item.path));
    let child;
    try { child = await readFile(resolve(sourceDirectory, included), 'utf8'); }
    catch { throw Object.assign(new Error(`cannot read included source: ${included}`), { code: 'FS_ERR' }); }
    parts.push(...await expandSessionIncludes(module, sourceDirectory, included, child, lang, [...active, identity], true, validate));
    start = item.span.end;
  }
  parts.push({ name, text: new TextDecoder().decode(bytes.subarray(start)), offset: start });
  return parts;
}

async function runFmt(module, inv) {
  if (inv.files.length === 0) {
    stdout.write(await module.format(inv.lang, '<stdin>', await readStdin(), inv.indent, inv.indentWidth));
    return 0;
  }
  let different = false;
  for (const file of inv.files) {
    let text;
    try {
      text = await readFile(file, 'utf8');
    } catch (error) {
      return failure('FS_ERR', `cannot read source: ${file}`);
    }
    const bytes = await module.format(inv.lang, file, text, inv.indent, inv.indentWidth);
    if ((inv.check || inv.inPlace) && new TextDecoder().decode(bytes) === text) continue;
    if (inv.check) {
      stdout.write(`${file}\n`);
      different = true;
      continue;
    }
    if (inv.inPlace) {
      try {
        await writeFileAtomic(file, bytes);
      } catch (error) {
        return failure('FS_ERR', `cannot replace source: ${file}`);
      }
      continue;
    }
    stdout.write(bytes);
  }
  return different ? 1 : 0;
}

async function runPlan(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  for (const target of targets) {
    stdout.write(await module.planJSON(source.compiled, target, false, source.name));
  }
  return 0;
}

async function runGraph(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  if (targets.length !== 1) return usageError('OPT_VALUE_INVALID', `${inv.name} requires exactly one target`);
  const kind = inv.name === 'inputs' ? 0 : inv.name === 'outputs' ? 1 : 2;
  let context;
  if (inv.expand) {
    context = contextFor(inv);
    context.inspection = true;
    context.grants.write = false;
    context.grants.run = false;
    context.inspectionGrants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }];
  }
  stdout.write(await module.graphJSON(source.compiled, targets[0], inv.depth, kind, inv.expand, source.name, context));
  return 0;
}

async function runTools(module, inv, sourceDirectory) {
  const check = inv.targets[0] === 'check';
  const targets = check ? inv.targets.slice(1) : inv.targets;
  if (!check && targets.length !== 0) return usageError('OPT_VALUE_INVALID', 'tools does not accept targets');
  if (check && targets.length === 0) return usageError('OPT_VALUE_INVALID', 'tools check requires at least one target');
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  if (check) {
    let failed = false;
    for (const target of targets) {
      const context = contextFor(inv);
      context.inspection = true;
      context.grants.write = false;
      context.grants.run = false;
      context.inspectionGrants = inv.grants?.length ? inv.grants : inv.noDefaultGrants ? [] : [{ capability: 'read', names: [process.cwd()] }, { capability: 'write', names: [process.cwd()] }, { capability: 'run', names: [] }];
      const text = module.decode0(await module.toolsCheck(source.compiled, target, source.name, context));
      for (const line of text.split('\n').filter(Boolean)) {
        const event = JSON.parse(line);
        if (inv.json) stdout.write(`${line}\n`);
        else stderr.write(renderDiagnostic(event.diagnostic, source, 80));
        failed = true;
      }
    }
    return failed ? 1 : 0;
  }
  const names = await module.toolNames(source.compiled, source.name);
  stdout.write(`${JSON.stringify(names.map((name) => ({ name, path: resolveTool(name) })))}\n`);
  return 0;
}

async function runCat(module, inv, sourceDirectory) {
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  if (targets.length !== 1) return usageError('OPT_VALUE_INVALID', 'cat requires exactly one target');
  const target = targets[0];
  try {
    const { kind, bytes } = await module.materialize(source.compiled, target, contextFor(inv), source.name);
    if (kind === 1) {
      stdout.write(bytes);
      return 0;
    }
    if (kind === 2) {
      const name = new TextDecoder().decode(bytes);
      try {
        stdout.write(await readFile(name));
        return 0;
      } catch (error) {
        return failure('FS_ERR', `cannot read artifact: ${error.message}`);
      }
    }
    return failure('NO_ARTIFACT', 'target has no readable artifact');
  } catch (error) {
    if (error.code === 'TGT_NO_RULE' && isPathTarget(target) && existsSync(target)) {
      stdout.write(await readFile(target));
      return 0;
    }
    throw error;
  }
}

async function runPrimary(module, inv, noArguments, sourceDirectory) {
  if (inv.watch) return featureUnsupported("--watch requires the native POSIX backend");
  const source = await discoverBuildSource(module, inv, sourceDirectory);
  if (source === null) {
    if (noArguments) {
      usage();
      return 0;
    }
    return failure('BUILD_NO_SOURCE', 'no build source found');
  }
  primarySource = source;
  buildProgress = { active: 0, completed: 0, failed: 0 };
  buildStartedAt = Date.now();
  const context = contextFor(inv);
  context.human = inv.json !== true;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  let failed = false;
  for (const target of targets) {
    try {
      lastDiagnostic = null;
      const { kind, bytes } = await module.materialize(source.compiled, target, context, source.name);
      if (kind === 1) stdout.write(bytes);
    } catch (error) {
      if (inv.json === true) throw error;
      if (error.diagnostics) {
        for (const detail of error.diagnostics) stderr.write(renderDiagnostic(detail, primarySource, 80));
        return 1;
      }
      failed = true;
      const detail = error.diagnostics?.[0] ?? lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
      if (!error.diagnostics && !lastDiagnostic && error.span !== undefined) { detail.source = source.name; detail.span = error.span; }
      if (!detail.target && detail.code !== 'PARSE_ERR') detail.target = target;
      stderr.write(renderDiagnostic(detail, primarySource, 80));
    }
  }
  if (inv.json !== true && buildProgress.completed + buildProgress.failed !== 0) printSummary();
  return failed ? 1 : 0;
}

async function dispatch(module, inv, noArguments) {
  jsonMode = inv.json === true;
  lastDiagnostic = null;
  buildProgress = null;
  primarySource = null;
  sourceTexts.clear();
  diagnosticFormat = inv.diagnosticFormat === 'human' ? 'human' : 'plain';
  diagnosticColor = resolveColor(inv.color, diagnosticFormat);
  const sourceDirectory = process.cwd();
  applyDirectory(inv);
  if (inv.name === 'help') {
    usage();
    return 0;
  }
  if (inv.name === 'run' || inv.name === 'render') return runSession(module, inv, sourceDirectory);
  if (inv.name === 'parse') return runParse(module, inv);
  if (inv.name === 'fmt') return runFmt(module, inv);
  if (inv.name === 'plan') return runPlan(module, inv, sourceDirectory);
  if (inv.name === 'inputs' || inv.name === 'outputs' || inv.name === 'span') return runGraph(module, inv, sourceDirectory);
  if (inv.name === 'tools') return runTools(module, inv, sourceDirectory);
  if (inv.name === 'cat') {
    if (inv.dryRun) return featureUnsupported('--dry-run');
    return runCat(module, inv, sourceDirectory);
  }
  if (inv.dryRun) return featureUnsupported('--dry-run');
  return runPrimary(module, inv, noArguments === true, sourceDirectory);
}

async function main() {
  const args = argv.slice(2);
  const first = args[0];

  if (first === '-V' || first === '--version') {
    stdout.write(`kame ${version()}\n`);
    return 0;
  }
  if (first === '-h' || first === '--help') {
    usage();
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
  if (first === 'do') {
    const command = args[1];
    if (command === undefined) {
      usage();
      return 0;
    }
    const inv = await module.parseCLI(command, args.slice(2));
    if (!inv.ok) {
      diagnostic(inv.error.code, inv.error.message);
      return 2;
    }
    return dispatch(module, inv);
  }
  const inv = await module.parseCLI('', args);
  if (!inv.ok) {
    diagnostic(inv.error.code, inv.error.message);
    return 2;
  }
  return dispatch(module, inv, args.length === 0);
}

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
      if (buildProgress !== null && buildProgress.completed + buildProgress.failed !== 0) printSummary();
    }
    process.exitCode = 1;
  },
);
