#!/usr/bin/env node
// Kame JavaScript CLI: the WebAssembly counterpart of the native CLI in
// docs/spec/009-cli.md. One ESM file, Node 18+, no third-party dependencies,
// an adjacent kame.wasm. See docs/spec/010-wasm.md.
//
// The command grammar (options, defaults, arity, conflicts, usage diagnostics)
// is parsed by the shared portable `cli` package inside the module via
// kame_wasm_cli, so the native CLI and this wrapper accept the same words. This
// file supplies only host capabilities and stream/exit policy.
import { readFile, stat, writeFile } from 'node:fs/promises';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { constants as osConstants } from 'node:os';
import process, { argv, env, stderr, stdout } from 'node:process';

// Children run in their own process group so a signal terminates the whole
// tree, and a forced termination reports 128 plus the signal number.
const activeChildren = new Set();

function installSignals() {
  for (const signal of ['SIGINT', 'SIGTERM']) {
    process.on(signal, () => {
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
      process.exit(128 + (osConstants.signals[signal] ?? 0));
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
  for (let i = lineStart; i < start;) {
    if (text[i] === '\t') { out += '\t'; i++; continue; }
    const rune = runeAt(text, i);
    out += ' '.repeat(displayWidth(rune.code));
    i += rune.size;
  }
  if (end < start) end = start;
  if (end > lineEnd) end = lineEnd;
  let width = 0;
  for (let i = start; i < end;) {
    if (text[i] === '\t') { width += 8; i++; continue; }
    const rune = runeAt(text, i);
    width += displayWidth(rune.code);
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
    const pos = sourcePosition(renderSource.text, span.start);
    out += `${d.source}:${pos.line}:${pos.column}: ${severityName(d.severity)} ${d.code}: ${d.message}\n`;
    let start = span.start;
    if (start < 0) start = 0;
    if (start > renderSource.text.length) start = renderSource.text.length;
    const [lineStart, lineEnd] = lineBounds(renderSource.text, start);
    out += wrappedExcerpt(renderSource.text, lineStart, lineEnd, start, span.end, width);
  } else if (d.source) {
    out += `${d.source}: ${severityName(d.severity)} ${d.code}: ${d.message}\n`;
  } else {
    out += `${severityName(d.severity)} ${d.code}: ${d.message}\n`;
  }
  for (const note of d.notes ?? []) out += `note: ${note}\n`;
  for (const related of d.related ?? []) {
    out += 'note: ';
    if (related.source) {
      if (renderSource && renderSource.name === related.source) {
        const pos = sourcePosition(renderSource.text, (related.span ?? { start: 0 }).start);
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
      if (renderSource && renderSource.name === frame.source) {
        const pos = sourcePosition(renderSource.text, (frame.span ?? { start: 0 }).start);
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
  stdout.write('  do plan         print the resolved plan as JSON\n');
  stdout.write('  do inputs       list declared input paths (--depth N)\n');
  stdout.write('  do outputs      list declared output paths (--depth N)\n');
  stdout.write('  do span         show transitive inputs and outputs (--expand, --depth N)\n');
  stdout.write('  do tools        list globally referenced build tools\n');
  stdout.write('  do parse        parse a language file and print a JSON AST\n');
  stdout.write('  do fmt          format source in place (-i) or check it (-n)\n');
  stdout.write('  do expr         evaluate a standalone expression\n');
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
  const names = [];
  collectPaths(globRoot(pattern), names);
  const matches = [];
  for (const name of names) {
    if (matchSegments(pattern, 0, name, 0)) matches.push(name.startsWith('/') ? name : `./${name}`);
  }
  matches.sort();
  return matches;
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
    const candidate = directory === '' ? join(process.cwd(), name) : join(directory, name);
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
        return this.completeFailure(instance, request, 'FS_ERR', `cannot read file: ${error.message}`);
      }
    }
    if (kind === 5) {
      if (!grants.read) return this.deny(instance, request);
      try {
        const info = await stat(payload);
        return this.completeJSON(instance, request, { name: payload, size: info.size, mode: info.mode, dir: info.isDirectory() });
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot stat file: ${error.message}`);
      }
    }
    if (kind === 7) {
      if (!grants.read) return this.deny(instance, request);
      return this.completeJSON(instance, request, existsSync(payload));
    }
    if (kind === 6) {
      if (!grants.read) return this.deny(instance, request);
      return this.completeJSON(instance, request, wildcardPaths(payload));
    }
    if (kind === 2) {
      if (!grants.write) return this.deny(instance, request);
      try {
        await writeFile(payload, data);
        return this.exports.kame_wasm_complete_nil(instance, request);
      } catch (error) {
        return this.completeFailure(instance, request, 'FS_ERR', `cannot write file: ${error.message}`);
      }
    }
    if (kind === 4) {
      if (!grants.env) return this.deny(instance, request);
      const value = env[payload];
      if (value === undefined) return this.exports.kame_wasm_complete_nil(instance, request);
      const encoded = this.write(value);
      return this.exports.kame_wasm_complete_text(instance, request, encoded.pointer, encoded.length);
    }
    if (kind === 3) {
      if (!grants.run) return this.deny(instance, request);
      if (context.streaming) {
        await runProcess(this, instance, payload, context);
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
        mkdirSync(dirname(path), { recursive: true });
        writeFileSync(path, record);
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

  async prepared(source, run, name) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const compiled = this.write(source);
      if (name !== undefined && name !== '') this.setSourceName(instance, name);
      if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      if (this.exports.kame_wasm_prepare(instance) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'PARSE_ERR');
      return this.copyQuery(run(instance), instance);
    } finally {
      this.exports.kame_wasm_instance_free(instance);
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

  async graphJSON(source, target, depth, kind, expand, name) {
    return this.prepared(source, (instance) => {
      const targetBytes = this.write(target);
      const flag = expand ? 1 : 0;
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_graph(handle, targetBytes.pointer, targetBytes.length, depth, kind, flag, dst, dstLen, lengthPointer);
    }, name);
  }

  async toolNames(source, name) {
    const bytes = await this.prepared(source, () => {
      return (handle, dst, dstLen, lengthPointer) => this.exports.kame_wasm_tools(handle, dst, dstLen, lengthPointer);
    }, name);
    return JSON.parse(this.decode0(bytes));
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
      if (json) {
        stdout.write(bytes);
        const event = JSON.parse(this.decode0(bytes));
        if (event.diagnostic && (event.type === 'target-failed' || event.type === 'target-cancelled')) lastDiagnostic = event.diagnostic;
      } else if (human) {
        humanEvent(bytes);
      }
    }
  }

  processStarted(instance) {
    if (this.exports.kame_wasm_process_started(instance) !== 0) throw new Error('process start event failed');
  }

  processStream(instance, stderrStream, chunk) {
    const pointer = this.allocate(chunk.length || 1);
    new Uint8Array(this.exports.memory.buffer, pointer, chunk.length).set(chunk);
    if (this.exports.kame_wasm_process_stream(instance, stderrStream ? 1 : 0, pointer, chunk.length) !== 0) throw new Error('process stream event failed');
  }

  processTerminal(instance, outcome, status, signal, stdoutBytes, stderrBytes, code, message) {
    const stdout = this.write(stdoutBytes ?? new Uint8Array(0));
    const stderr = this.write(stderrBytes ?? new Uint8Array(0));
    const codeBytes = this.write(code);
    const messageBytes = this.write(message);
    if (this.exports.kame_wasm_process_terminal(instance, status, signal, outcome, stdout.pointer, stdout.length, stderr.pointer, stderr.length, codeBytes.pointer, codeBytes.length, messageBytes.pointer, messageBytes.length) !== 0) throw new Error('process terminal event failed');
  }

  async materialize(source, target, context, name) {
    const instance = this.exports.kame_wasm_instance_create();
    if (instance === 0n) throw Object.assign(new Error('cannot create WASM instance'), { code: 'NO_MEMORY' });
    try {
      const compiled = this.write(source);
      if (name !== undefined && name !== '') this.setSourceName(instance, name);
      if (this.exports.kame_wasm_source_compile(instance, compiled.pointer, compiled.length) !== 0) throw this.compileFailure(instance, 'PARSE_ERR');
      const directoryBytes = this.write(process.cwd());
      if (this.exports.kame_wasm_set_directory(instance, directoryBytes.pointer, directoryBytes.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'HOST_FAIL');
      if (this.exports.kame_wasm_set_forwarding(instance, 1) !== 0) throw Object.assign(new Error('request forwarding is unavailable'), { code: 'FEATURE_UNSUP' });
      context.streaming = true;
      const encoded = this.write(target);
      if (this.exports.kame_wasm_target_begin(instance, encoded.pointer, encoded.length) !== 0) throw diagnosticError(this.instanceDiagnostic(instance), 'TGT_NO_RULE');
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

// runProcess runs a recipe on the host, streaming stdout/stderr back to the
// engine as process events. It honors --timeout (killing an overrunning child)
// and --retry (rerunning a failed attempt) before reporting a terminal status.
// The terminal report carries the wait status and captured output so the engine
// reconstructs the native completion value and failure cause.
async function runProcess(module, instance, script, context) {
  let attempt = 0;
  for (;;) {
    const outcome = await runProcessAttempt(module, instance, script, context);
    if (outcome.ok) {
      module.processTerminal(instance, 0, 0, 0, outcome.stdout, outcome.stderr, '', '');
      return;
    }
    if (outcome.retryable && attempt < context.retryCount) {
      attempt++;
      continue;
    }
    module.processTerminal(instance, outcome.outcome, outcome.status, outcome.signal, outcome.stdout, outcome.stderr, outcome.code, outcome.message);
    return;
  }
}

function runProcessAttempt(module, instance, script, context) {
  return new Promise((resolveAttempt) => {
    const shell = context.shell.length !== 0 ? context.shell : ['/bin/sh', '-c'];
    const childEnv = { ...process.env };
    for (const entry of context.environment) {
      const at = entry.indexOf('=');
      if (at > 0) childEnv[entry.slice(0, at)] = entry.slice(at + 1);
    }
    const child = spawn(shell[0], [...shell.slice(1), script], { stdio: ['ignore', 'pipe', 'pipe'], env: childEnv, detached: true });
    track(child);
    module.processStarted(instance);
    const out = [];
    const err = [];
    let failure = null;
    let timedOut = false;
    let timer = null;
    if (context.timeoutMS > 0) {
      timer = setTimeout(() => { timedOut = true; child.kill('SIGKILL'); }, context.timeoutMS);
    }
    child.stdout.on('data', (chunk) => { out.push(chunk); module.processStream(instance, false, chunk); });
    child.stderr.on('data', (chunk) => { err.push(chunk); module.processStream(instance, true, chunk); });
    child.once('error', (error) => { failure = error; });
    child.once('close', (code, signalName) => {
      if (timer !== null) clearTimeout(timer);
      const stdout = Buffer.concat(out);
      const stderr = Buffer.concat(err);
      let status = 0;
      let signal = 0;
      if (code !== null) status = code;
      if (signalName !== null) signal = osConstants.signals[signalName] ?? 0;
      if (timedOut) resolveAttempt({ ok: false, outcome: 1, status: 0, signal, code: 'RECIPE_TIMEOUT', message: 'recipe timed out', stdout, stderr, retryable: false });
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
  return {
    grants: effectiveGrants(inv),
    shell: inv.shell ?? [],
    environment: inv.environment ?? [],
    json: inv.json === true,
    timeoutMS: inv.timeoutMS ?? 0,
    retryCount: inv.retryCount ?? 0,
  };
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
  if (inv.command) return { name: '<command>', text: inv.command };
  if (inv.file) {
    try {
      return { name: inv.file, text: await readFile(inv.file, 'utf8') };
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

async function runExpr(module, inv) {
  let expression = inv.command ?? '';
  if (!expression && inv.file) {
    try {
      expression = await readFile(inv.file, 'utf8');
    } catch (error) {
      return failure('FS_ERR', `cannot read expression: ${inv.file}`);
    }
  }
  if (!expression) expression = await readStdin();
  stdout.write(await module.evaluate('', expression, contextFor(inv)));
  return 0;
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
    if (new TextDecoder().decode(bytes) === text) continue;
    if (inv.check) {
      stdout.write(`${file}\n`);
      different = true;
      continue;
    }
    if (inv.inPlace) {
      try {
        await writeFile(file, bytes);
      } catch (error) {
        return failure('FS_ERR', `cannot replace source: ${file}`);
      }
      continue;
    }
    stdout.write(bytes);
  }
  return different ? 1 : 0;
}

async function runPlan(module, inv) {
  const source = await discoverSource(inv);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  for (const target of targets) {
    stdout.write(await module.planJSON(source.text, target, false, source.name));
  }
  return 0;
}

async function runGraph(module, inv) {
  const source = await discoverSource(inv);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  if (targets.length !== 1) return usageError('OPT_VALUE_INVALID', `${inv.name} requires exactly one target`);
  const kind = inv.name === 'inputs' ? 0 : inv.name === 'outputs' ? 1 : 2;
  stdout.write(await module.graphJSON(source.text, targets[0], inv.depth, kind, inv.expand, source.name));
  return 0;
}

async function runTools(module, inv) {
  if (inv.targets.length !== 0) return usageError('OPT_VALUE_INVALID', 'tools does not accept targets');
  const source = await discoverSource(inv);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const names = await module.toolNames(source.text, source.name);
  stdout.write(`${JSON.stringify(names.map((name) => ({ name, path: resolveTool(name) })))}\n`);
  return 0;
}

async function runCat(module, inv) {
  const source = await discoverSource(inv);
  if (source === null) return failure('BUILD_NO_SOURCE', 'no build source found');
  primarySource = source;
  const targets = inv.targets.length !== 0 ? inv.targets : ['default'];
  if (targets.length !== 1) return usageError('OPT_VALUE_INVALID', 'cat requires exactly one target');
  const target = targets[0];
  try {
    const { kind, bytes } = await module.materialize(source.text, target, contextFor(inv), source.name);
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

async function runPrimary(module, inv, noArguments) {
  const source = await discoverSource(inv);
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
      const { kind, bytes } = await module.materialize(source.text, target, context, source.name);
      if (kind === 1) stdout.write(bytes);
    } catch (error) {
      if (inv.json === true) throw error;
      failed = true;
      const detail = lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
      if (error.span !== undefined) { detail.source = source.name; detail.span = error.span; }
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
  diagnosticFormat = inv.diagnosticFormat === 'human' ? 'human' : 'plain';
  diagnosticColor = resolveColor(inv.color, diagnosticFormat);
  applyDirectory(inv);
  if (inv.name === 'help') {
    usage();
    return 0;
  }
  if (inv.name === 'expr') return runExpr(module, inv);
  if (inv.name === 'parse') return runParse(module, inv);
  if (inv.name === 'fmt') return runFmt(module, inv);
  if (inv.name === 'plan') return runPlan(module, inv);
  if (inv.name === 'inputs' || inv.name === 'outputs' || inv.name === 'span') return runGraph(module, inv);
  if (inv.name === 'tools') return runTools(module, inv);
  if (inv.name === 'cat') {
    if (inv.dryRun) return featureUnsupported('--dry-run');
    return runCat(module, inv);
  }
  if (inv.dryRun) return featureUnsupported('--dry-run');
  return runPrimary(module, inv, noArguments === true);
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
    if (jsonMode) {
      diagnostic(error.code ?? 'HOST_FAIL', error.message);
    } else {
      const detail = lastDiagnostic ?? { code: error.code ?? 'HOST_FAIL', severity: 'error', message: error.message };
      if (error.span !== undefined && primarySource) { detail.source = primarySource.name; detail.span = error.span; }
      stderr.write(renderDiagnostic(detail, primarySource, 80));
      if (buildProgress !== null && buildProgress.completed + buildProgress.failed !== 0) printSummary();
    }
    process.exitCode = 1;
  },
);
