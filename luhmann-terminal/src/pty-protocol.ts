// The browser pty client's decision logic, factored out of the element as a
// pure module (R3162): endpoint derivation, control-frame and input encoding,
// the close→probe→action classification, and the backoff schedule. Imports
// neither DOM nor WebSocket, so every rule the terminal follows is testable
// without a browser (pty-protocol.test.ts).
//
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md | R3162

/** ark's own websocket for the hosted pty (R3141). */
export const ptyPath = '/luhmann/pty';

/** The hosted/session report, mirrored onto the UI mux for us (R3149). */
export const statusPath = '/luhmann/pty-status';

/** Reconnect schedule (R3157): double from base to cap, then spread by jitter. */
export const backoffBaseMs = 1000;
export const backoffCapMs = 30000;
export const backoffJitter = 0.25;

/** What the terminal is doing, as announced to the host (R3159). */
export type TerminalState = 'connecting' | 'connected' | 'waiting' | 'asleep';

/** The result of asking `/luhmann/pty-status` why the socket closed (R3157). */
export type ProbeOutcome =
	| { kind: 'hosted'; session: string }
	| { kind: 'asleep' }
	| { kind: 'unreachable' };

/** What to do about it: stop, or wait this long and retry (R3157). */
export type Action = { state: 'asleep' } | { state: 'waiting'; delayMs: number };

/** xterm options derived from ark's theme (R3155). */
export interface ThemeOptions {
	theme: { background: string; foreground: string; cursor: string };
	fontFamily: string;
}

// Fallbacks for a page that has not loaded ark's theme. An unset custom
// property reads as '', which xterm would take literally and render an
// invisible terminal — so nothing empty may reach it.
const themeFallback = {
	bg: '#0a0a0f',
	text: '#dddddd',
	accent: '#E07A47',
	mono: 'monospace',
};

// Derive the websocket URL from the page's own origin. Not configurable:
// R3141's upgrader is same-origin, so no other endpoint could connect.
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#1.5 | R3151
export function ptyEndpoint(loc: { protocol: string; host: string }): string {
	const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:';
	return `${scheme}//${loc.host}${ptyPath}`;
}

// The resize control frame (R3143). The element sends this first, always — a
// connection opening with anything else is closed by the host (R3152).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#1.9 | R3152
export function resizeFrame(cols: number, rows: number): string {
	return JSON.stringify({ t: 'resize', cols, rows });
}

// The repaint control frame (R3143): ark holds no virtual screen, so a freshly
// attached client asks the child to redraw rather than sit blank (R3152).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#1.11 | R3152
export function repaintFrame(): string {
	return JSON.stringify({ t: 'repaint' });
}

const utf8 = new TextEncoder();

// xterm's onData yields a JS string; the pty takes bytes (R3153).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#4.1 | R3153
export function encodeData(s: string): Uint8Array {
	return utf8.encode(s);
}

// xterm's onBinary yields byte-per-character (mouse reports, some paste
// paths), so UTF-8 encoding it would corrupt every char above 0x7f (R3153).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#4.2 | R3153
export function encodeBinary(s: string): Uint8Array {
	const out = new Uint8Array(s.length);
	for (let i = 0; i < s.length; i++) {
		out[i] = s.charCodeAt(i) & 0xff;
	}
	return out;
}

// Read a `/luhmann/pty-status` body. A malformed one reports unreachable
// rather than throwing: a garbled answer from a bouncing server is not
// evidence the session ended, and a throw here would strand the element in the
// close handler (R3157).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#2.2 | R3157
export function parseProbe(body: string): ProbeOutcome {
	try {
		const o: unknown = JSON.parse(body);
		if (o && typeof o === 'object' && typeof (o as { hosted?: unknown }).hosted === 'boolean') {
			const r = o as { hosted: boolean; session?: unknown };
			return r.hosted ? { kind: 'hosted', session: String(r.session ?? '') } : { kind: 'asleep' };
		}
	} catch {
		// fall through to unreachable
	}
	return { kind: 'unreachable' };
}

// Classify a probe into the element's next move (R3157). `asleep` stops: the
// session ended, no reconnect brings it back, and waiting on one that would
// have to be started is exactly what R3158/R3114 forbid. Both other arms wait
// — a bounce is a wait condition, not an error (Stubborn Plumbing).
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#2.4 | R3157, R3158
export function nextAction(probe: ProbeOutcome, attempt: number, rand: () => number): Action {
	if (probe.kind === 'asleep') {
		return { state: 'asleep' };
	}
	return { state: 'waiting', delayMs: backoffDelay(attempt, rand) };
}

// The jittered exponential schedule (R3157). `rand` is injected so the spread
// is testable rather than flaky. Clamped after jitter, so the cap is a real
// ceiling.
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#2.7 | R3157
export function backoffDelay(attempt: number, rand: () => number): number {
	const exp = Math.min(backoffBaseMs * 2 ** Math.max(0, attempt), backoffCapMs);
	const jittered = exp * (1 + backoffJitter * (2 * rand() - 1));
	return Math.min(Math.round(jittered), backoffCapMs);
}

// Map ark's --term-* variables onto xterm's options through an injected lookup
// (R3155). The 16 ANSI colors are left at xterm's defaults: that palette is the
// child's own output, not the app's chrome, so an ark theme must not re-hue it.
// CRC: crc-PtyProtocol.md | Seq: seq-luhmann-terminal.md#1.1 | R3155
export function themeOptions(lookup: (name: string) => string): ThemeOptions {
	const read = (name: string, fallback: string): string => lookup(name).trim() || fallback;
	return {
		theme: {
			background: read('--term-bg', themeFallback.bg),
			foreground: read('--term-text', themeFallback.text),
			cursor: read('--term-accent', themeFallback.accent),
		},
		fontFamily: read('--term-mono', themeFallback.mono),
	};
}
