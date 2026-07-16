// Unit tests for the browser pty client's pure core. Run with `node --test`
// (Node strips the types natively — no runner, no dependency). The element's
// DOM/socket surface needs a real browser and is #40's smoke-test family; every
// rule it follows is here.
//
// CRC: crc-PtyProtocol.md | R3162

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
	backoffBaseMs,
	backoffCapMs,
	backoffDelay,
	backoffJitter,
	encodeBinary,
	encodeData,
	nextAction,
	parseProbe,
	ptyEndpoint,
	repaintFrame,
	resizeFrame,
	themeOptions,
} from './pty-protocol.ts';

// --- endpoint derivation (R3151) --------------------------------------------

test('endpoint is ws on http', () => {
	assert.equal(
		ptyEndpoint({ protocol: 'http:', host: 'localhost:8080' }),
		'ws://localhost:8080/luhmann/pty',
	);
});

test('endpoint is wss on https', () => {
	// A secure page must not open an insecure socket — the browser blocks it.
	assert.equal(
		ptyEndpoint({ protocol: 'https:', host: 'ark.example:443' }),
		'wss://ark.example:443/luhmann/pty',
	);
});

// --- control frames (R3152, R3143) ------------------------------------------

test('resize frame matches the wire', () => {
	// The shape Go's parsePtyControl decodes; an unknown `t` errors there.
	assert.deepEqual(JSON.parse(resizeFrame(80, 24)), { t: 'resize', cols: 80, rows: 24 });
});

test('repaint frame matches the wire', () => {
	assert.deepEqual(JSON.parse(repaintFrame()), { t: 'repaint' });
});

// --- input encoding (R3153) -------------------------------------------------

test('encodeData is UTF-8', () => {
	assert.deepEqual([...encodeData('é')], [0xc3, 0xa9]);
	assert.deepEqual([...encodeData('hello')], [0x68, 0x65, 0x6c, 0x6c, 0x6f]);
});

test('encodeBinary masks to the low byte', () => {
	// onBinary is byte-per-char: 'é' is one byte (0xe9), not UTF-8's two.
	assert.deepEqual([...encodeBinary('\x1b[Mé')], [0x1b, 0x5b, 0x4d, 0xe9]);
});

test('Ctrl-] encodes as ordinary input', () => {
	// R3156: the CLI's detach key is not special here — it must reach the child.
	assert.deepEqual([...encodeData('\x1d')], [0x1d]);
});

// --- probe parsing (R3157) --------------------------------------------------

test('parseProbe reads a hosted session', () => {
	// The shape handleLuhmannStatus actually writes.
	assert.deepEqual(parseProbe('{"hosted":true,"session":"6e4bed60","secretaries":2}'), {
		kind: 'hosted',
		session: '6e4bed60',
	});
});

test('parseProbe reads an unhosted server', () => {
	assert.deepEqual(parseProbe('{"hosted":false,"session":"","secretaries":0}'), { kind: 'asleep' });
});

test('parseProbe treats a malformed body as unreachable', () => {
	// A half-written response from a bouncing server must not throw out of the
	// close handler. Unreachable retries — a garbled answer is not evidence the
	// session ended.
	assert.deepEqual(parseProbe('<html>502 Bad Gateway'), { kind: 'unreachable' });
	assert.deepEqual(parseProbe(''), { kind: 'unreachable' });
	assert.deepEqual(parseProbe('{"hosted":"yes"}'), { kind: 'unreachable' });
});

// --- the three-way classification (R3157, R3158) ----------------------------

const midJitter = (): number => 0.5;

test('not hosted stops the loop', () => {
	// The session ended; reconnecting cannot bring it back, and the element must
	// never wait on a session that would have to be started (R3114).
	assert.deepEqual(nextAction({ kind: 'asleep' }, 0, midJitter), { state: 'asleep' });
});

test('hosted retries', () => {
	const a = nextAction({ kind: 'hosted', session: 'S' }, 0, midJitter);
	assert.equal(a.state, 'waiting');
	assert.ok(a.state === 'waiting' && a.delayMs > 0);
});

test('unreachable retries', () => {
	// Stubborn Plumbing: a bounce is a wait condition, not an error — the same
	// arm as hosted, deliberately.
	const a = nextAction({ kind: 'unreachable' }, 0, midJitter);
	assert.equal(a.state, 'waiting');
	assert.ok(a.state === 'waiting' && a.delayMs > 0);
});

// --- backoff (R3157) --------------------------------------------------------

test('backoff doubles and caps', () => {
	assert.equal(backoffDelay(0, midJitter), backoffBaseMs);
	assert.equal(backoffDelay(1, midJitter), backoffBaseMs * 2);
	assert.equal(backoffDelay(2, midJitter), backoffBaseMs * 4);
	assert.equal(backoffDelay(100, midJitter), backoffCapMs);

	let prev = 0;
	for (let n = 0; n <= 100; n++) {
		const d = backoffDelay(n, midJitter);
		assert.ok(d >= prev, `attempt ${n}: ${d} < ${prev}`);
		assert.ok(d <= backoffCapMs, `attempt ${n}: ${d} above cap`);
		prev = d;
	}
});

test('backoff jitter stays in band', () => {
	// The injected source is what makes this deterministic rather than flaky.
	const low = backoffDelay(0, () => 0);
	const high = backoffDelay(0, () => 1);
	assert.ok(low > 0, 'a zero or negative delay would busy-loop');
	assert.equal(low, Math.round(backoffBaseMs * (1 - backoffJitter)));
	assert.equal(high, Math.round(backoffBaseMs * (1 + backoffJitter)));
});

// --- theming (R3155) --------------------------------------------------------

test('theme options map ark variables', () => {
	const vars: Record<string, string> = {
		'--term-bg': '#101018',
		'--term-text': '#e0e0e0',
		'--term-accent': '#E07A47',
		'--term-mono': 'Iosevka, monospace',
	};
	const opts = themeOptions((n) => vars[n] ?? '');
	assert.deepEqual(opts, {
		theme: { background: '#101018', foreground: '#e0e0e0', cursor: '#E07A47' },
		fontFamily: 'Iosevka, monospace',
	});
	// The child's palette is not the app's chrome: no ANSI-16 keys are set.
	assert.deepEqual(Object.keys(opts.theme).sort(), ['background', 'cursor', 'foreground']);
});

test('theme options fall back', () => {
	// '' is what getPropertyValue gives for an unset custom property; xterm would
	// take it literally and render an invisible terminal.
	const opts = themeOptions(() => '');
	for (const v of Object.values(opts.theme)) {
		assert.ok(v.length > 0);
	}
	assert.ok(opts.fontFamily.length > 0);
});
