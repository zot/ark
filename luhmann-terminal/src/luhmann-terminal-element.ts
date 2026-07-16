// The <luhmann-terminal> custom element: a live terminal on the ark-hosted
// Luhmann session, driven over ark's own websocket (R3141) — the browser's
// `ark luhmann attach`. A composable primitive with no chrome of its own; the
// thin shell wiring PtyProtocol's rules to an xterm instance and a socket.
//
// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md | R3150

import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';
import xtermCss from '@xterm/xterm/css/xterm.css';

import {
	encodeBinary,
	encodeData,
	nextAction,
	parseProbe,
	ptyEndpoint,
	repaintFrame,
	resizeFrame,
	statusPath,
	themeOptions,
	type ProbeOutcome,
	type TerminalState,
} from './pty-protocol.ts';

const tagName = 'luhmann-terminal';
const statusEvent = 'luhmann-terminal-status';
const styleId = 'luhmann-terminal-xterm-css';

// R3154: a drag emits a continuous stream of box changes, and each one would
// otherwise become a TIOCSWINSZ plus a real SIGWINCH — the child re-rendering
// for sizes the user is passing through rather than choosing.
const resizeDebounceMs = 100;

// R3154: a custom element defaults to `display: inline`, and width/height do not
// apply to an inline box — so a host's `luhmann-terminal { height: 100% }` would
// be silently ignored, FitAddon would measure a collapsed box, and the terminal
// would come out a few cells wide. The element declares its own display so that
// the host's sizing CSS takes effect at all. It deliberately does NOT declare a
// size: a terminal has no intrinsic one, so the box stays the host's to give.
// Prepended, so a host rule of equal specificity still wins.
const hostCss = 'luhmann-terminal { display: block; }\n';

// Inject the element's display rule + xterm's stylesheet once per document
// (R3160). Idempotent: several terminals on a page share the one <style>.
// CRC: crc-LuhmannTerminalElement.md | R3154, R3160
function injectStyle(): void {
	if (document.getElementById(styleId)) {
		return;
	}
	const el = document.createElement('style');
	el.id = styleId;
	el.textContent = hostCss + xtermCss;
	document.head.appendChild(el);
}

export class LuhmannTerminalElement extends HTMLElement {
	// Element properties, not closure-captured variables: inspectable on the
	// live element from devtools, and they cannot go missing under DOM surgery.
	term: Terminal | null = null;
	fitAddon: FitAddon | null = null;
	ws: WebSocket | null = null;
	session = '';
	attempt = 0;
	retryTimer = 0;
	resizeTimer = 0;
	observer: ResizeObserver | null = null;

	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#1 | R3150, R3154, R3155
	connectedCallback(): void {
		injectStyle();
		const style = getComputedStyle(document.documentElement);
		const opts = themeOptions((name) => style.getPropertyValue(name));
		const term = new Terminal({
			theme: opts.theme,
			fontFamily: opts.fontFamily,
			cursorBlink: true,
		});
		const fitAddon = new FitAddon();
		term.loadAddon(fitAddon);
		term.open(this);
		// R3153: both input directions. R3156 needs no key handling — Ctrl-] rides
		// this same path as ordinary input.
		term.onData((s) => this.send(encodeData(s)));
		term.onBinary((s) => this.send(encodeBinary(s)));
		this.term = term;
		this.fitAddon = fitAddon;

		this.observer = new ResizeObserver(() => this.onResize());
		this.observer.observe(this);

		this.attempt = 0;
		void this.classify(true);
	}

	// This *is* detach: the session survives, as it does for any client (R3121).
	// No DEC-mode sanitize — R3140 restores the user's real terminal; here the
	// terminal is the instance being disposed and nothing outlives it (R3156).
	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#5 | R3156
	disconnectedCallback(): void {
		window.clearTimeout(this.retryTimer);
		window.clearTimeout(this.resizeTimer);
		this.observer?.disconnect();
		this.observer = null;
		const ws = this.ws;
		this.ws = null; // makes the pending onclose a no-op (identity check)
		ws?.close();
		this.term?.dispose();
		this.term = null;
		this.fitAddon = null;
	}

	// Ask why we are not attached, and act (R3157). Runs before every socket
	// open and again on every close. The probe is a cheap GET and always runs
	// immediately, which is what makes `asleep` instant rather than one backoff
	// late — and `asleep` is the common case, since Luhmann is usually not
	// running. `immediate` distinguishes a first attempt (connect now) from a
	// retry after a failure (back off first). Never calls launch: the element
	// attaches to what exists or reports that nothing does (R3158, R3114).
	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#2 | R3157, R3158
	private async classify(immediate: boolean): Promise<void> {
		const probe = await this.probe();
		if (!this.isConnected) {
			return; // detached mid-probe
		}
		const action = nextAction(probe, this.attempt, Math.random);
		if (action.state === 'asleep') {
			this.session = '';
			this.announce('asleep');
			return;
		}
		if (probe.kind === 'hosted') {
			this.session = probe.session;
			if (immediate) {
				this.openSocket();
				return;
			}
		}
		// Hosted but backing off after a failure, or ark unreachable: wait, then
		// re-probe — the world may change during the backoff, so the next attempt
		// re-decides rather than blindly opening a socket.
		this.announce('waiting');
		this.attempt++;
		this.retryTimer = window.setTimeout(() => void this.classify(true), action.delayMs);
	}

	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#2.1 | R3149, R3157
	private async probe(): Promise<ProbeOutcome> {
		try {
			const resp = await fetch(statusPath, { cache: 'no-store' });
			if (!resp.ok) {
				return { kind: 'unreachable' };
			}
			return parseProbe(await resp.text());
		} catch {
			return { kind: 'unreachable' }; // ark is down or bouncing
		}
	}

	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#1.5 | R3151, R3152, R3153
	private openSocket(): void {
		this.announce('connecting');
		const ws = new WebSocket(ptyEndpoint(location));
		ws.binaryType = 'arraybuffer';
		this.ws = ws;

		ws.onopen = () => {
			// R3152: the resize goes first — R3143 closes a connection that opens
			// with anything else, so a repaint-first element would never attach.
			// The repaint follows because ark holds no screen (R3115): without it
			// the terminal sits blank until the child next paints.
			this.sendSize(ws);
			ws.send(repaintFrame());
			this.attempt = 0;
			this.announce('connected');
		};
		ws.onmessage = (e) => {
			if (typeof e.data === 'string') {
				return; // R3153: the host sends no text; not a case to guess at
			}
			this.term?.write(new Uint8Array(e.data as ArrayBuffer));
		};
		ws.onclose = () => {
			if (this.ws !== ws) {
				return; // superseded or detached — not this socket's business
			}
			this.ws = null;
			void this.classify(false);
		};
		// onerror always precedes onclose, so close is the single handling point.
	}

	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#4.1 | R3153
	private send(bytes: Uint8Array): void {
		if (this.ws?.readyState === WebSocket.OPEN) {
			this.ws.send(bytes);
		}
	}

	// Reports only this client's size; the minimum across clients is the host's
	// business (smallest-wins, R3120).
	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#4.5 | R3154
	private sendSize(ws: WebSocket): void {
		this.fitAddon?.fit();
		const term = this.term;
		if (term) {
			ws.send(resizeFrame(term.cols, term.rows));
		}
	}

	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#4.5 | R3154
	private onResize(): void {
		window.clearTimeout(this.resizeTimer);
		this.resizeTimer = window.setTimeout(() => {
			const ws = this.ws;
			if (ws?.readyState === WebSocket.OPEN) {
				this.sendSize(ws);
			} else {
				this.fitAddon?.fit(); // keep the view right even while detached
			}
		}, resizeDebounceMs);
	}

	// The element's entire host interface: a host renders a lamp from this and
	// needs to know nothing else (R3159).
	// CRC: crc-LuhmannTerminalElement.md | Seq: seq-luhmann-terminal.md#2.6 | R3159
	private announce(state: TerminalState): void {
		this.dispatchEvent(
			new CustomEvent(statusEvent, {
				bubbles: true,
				composed: true,
				detail: { state, session: this.session, attempt: this.attempt },
			}),
		);
	}
}

if (!customElements.get(tagName)) {
	customElements.define(tagName, LuhmannTerminalElement);
}
