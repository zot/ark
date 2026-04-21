// CRC: crc-PdfChunkElement.md | Seq: seq-pdf-chunk-render.md, seq-pdf-slice.md
// R1666-R1668, R1677-R1680, R1691-R1702, R1709

import * as pdfjsLib from 'pdfjs-dist';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import {
	bandScale,
	findHost,
	pageKey,
	scaleBand,
	setupPdfHost,
	type PageImage,
	type PdfHostStorage,
} from './pdf-host';

// R1710: worker bundle served alongside the main bundle
pdfjsLib.GlobalWorkerOptions.workerSrc = '/pdf-chunk-element.worker.js';

interface Rect {
	x: number;
	y: number;
	w: number;
	h: number;
}

function parseRect(s: string | null): Rect | null {
	if (!s) return null;
	const parts = s.split(',').map((n) => parseFloat(n));
	if (parts.length !== 4 || parts.some(isNaN)) return null;
	const [x, y, w, h] = parts;
	return { x, y, w, h };
}

function formatRect(r: Rect): string {
	return `${r.x},${r.y},${r.w},${r.h}`;
}

// Hide the <ark-tag> button when the tag is positioned inside a
// <pdf-chunk>: in overlay mode the whole tag is clickable, not just
// the button.
const OVERLAY_STYLE_ID = 'pdf-chunk-overlay-styles';
function ensureOverlayStyles(): void {
	if (document.getElementById(OVERLAY_STYLE_ID)) return;
	const s = document.createElement('style');
	s.id = OVERLAY_STYLE_ID;
	s.textContent = `
		pdf-chunk { display: block; position: relative; overflow: hidden; background: var(--pdf-chunk-bg, white); }
		pdf-chunk > img.pdf-chunk-image { position: absolute; pointer-events: none; user-select: none; }
		pdf-chunk > ark-tag[rect] .ark-tag-action { display: none !important; }
		pdf-chunk > ark-tag[rect] { box-sizing: border-box; cursor: pointer; }
		pdf-chunk > .pdf-chunk-highlight {
			position: absolute;
			background: var(--term-accent-dim, rgba(224, 122, 71, 0.35));
			box-shadow: 0 0 0 1px var(--term-accent, #E07A47);
			border-radius: 2px;
			pointer-events: none;
			box-sizing: border-box;
		}
	`;
	document.head.appendChild(s);
}

// Shared rendering routines — operate on any element that has the
// PdfHostStorage properties. pdfjs-dist is imported only from this
// module, so the ark-search bundle stays pdfjs-free.

function hostGetDocument(host: PdfHostStorage, src: string): Promise<PDFDocumentProxy> {
	const existing = host.pdfDocCache.get(src) as Promise<PDFDocumentProxy> | undefined;
	if (existing) return existing;
	const fresh = pdfjsLib.getDocument(src).promise;
	host.pdfDocCache.set(src, fresh);
	return fresh;
}

function hostGetPageImage(
	host: PdfHostStorage,
	src: string,
	page: number,
	band: number,
): Promise<PageImage> {
	const key = pageKey(src, page, band);
	const existing = host.pdfPageCache.get(key);
	if (existing) return existing;
	const fresh = renderPage(host, src, page, band);
	host.pdfPageCache.set(key, fresh);
	return fresh;
}

async function renderPage(
	host: PdfHostStorage,
	src: string,
	page: number,
	band: number,
): Promise<PageImage> {
	const doc = await hostGetDocument(host, src);
	const pdfPage = await doc.getPage(page);
	const scale = bandScale(band);
	const viewport = pdfPage.getViewport({ scale });
	const canvas = document.createElement('canvas');
	canvas.width = Math.ceil(viewport.width);
	canvas.height = Math.ceil(viewport.height);
	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error('no 2d canvas context');
	await pdfPage.render({ canvasContext: ctx, viewport }).promise;
	const blob = await new Promise<Blob | null>((resolve) =>
		canvas.toBlob(resolve, 'image/png'),
	);
	if (!blob) throw new Error('canvas.toBlob failed');
	const url = URL.createObjectURL(blob);
	host.pdfBlobUrls.push(url);
	const baseViewport = pdfPage.getViewport({ scale: 1 });
	return { url, pageW: baseViewport.width, pageH: baseViewport.height };
}

export class PdfChunkElement extends HTMLElement {
	// Element properties — not closure-captured, inspectable in devtools,
	// survive DOM surgery (slice-and-insert).
	host: PdfHostStorage | null = null;
	imgEl: HTMLImageElement | null = null;
	errorEl: HTMLDivElement | null = null;
	currentBand: number | null = null;
	currentPageW = 0; // PDF points
	currentPageH = 0; // PDF points
	resizeObserver: ResizeObserver | null = null;
	pendingRender = 0; // monotonic token — cancels stale renders
	overlayClickBound = false;
	// Cached match rects (in chunker coords) from the last getTextContent
	// pass. Recomputed when src/page/highlight changes; re-placed (CSS-only)
	// on band-free resizes.
	highlightRects: Rect[] = [];
	highlightEls: HTMLDivElement[] = [];
	highlightKey = ''; // `${src}|${page}|${highlightAttr}` — skip recompute on identical

	static get observedAttributes(): string[] {
		return ['src', 'page', 'rect', 'page-size', 'highlight'];
	}

	connectedCallback(): void {
		ensureOverlayStyles();
		this.host = findHost(this);
		if (!this.host) {
			// Standalone fallback — install host on document.body so all
			// descendant chunks share the same caches within the page.
			this.host = setupPdfHost(document.body);
		}
		this.resizeObserver = new ResizeObserver(() => this.render());
		this.resizeObserver.observe(this);
		this.addOverlayClickHandler();
		this.render();
	}

	disconnectedCallback(): void {
		this.resizeObserver?.disconnect();
		this.resizeObserver = null;
	}

	attributeChangedCallback(): void {
		if (this.isConnected) this.render();
	}

	// Capture-phase click handler on the element itself — catches
	// clicks on any <ark-tag rect=""> child before ark-tag's own
	// button handler can run.
	addOverlayClickHandler(): void {
		if (this.overlayClickBound) return;
		this.overlayClickBound = true;
		this.addEventListener(
			'click',
			(ev) => this.handleClick(ev as MouseEvent),
			true,
		);
	}

	handleClick(ev: MouseEvent): void {
		const path = ev.composedPath();
		for (const node of path) {
			if (!(node instanceof HTMLElement)) continue;
			if (node === this) return;
			if (node.tagName.toLowerCase() !== 'ark-tag') continue;
			if (!node.hasAttribute('rect')) continue;
			const tagRect = parseRect(node.getAttribute('rect'));
			const chunkRect = parseRect(this.getAttribute('rect'));
			if (!tagRect || !chunkRect) return;
			ev.preventDefault();
			ev.stopImmediatePropagation();
			// Fire the informational event for any listeners.
			const nameEl = node.querySelector('name');
			const valueEl = node.querySelector('value');
			node.dispatchEvent(
				new CustomEvent('ark-tag-click', {
					bubbles: true,
					detail: {
						name: nameEl?.textContent ?? '',
						value: valueEl?.textContent ?? '',
					},
				}),
			);
			this.sliceAtTag(node, tagRect, chunkRect);
			return;
		}
	}

	async render(): Promise<void> {
		const token = ++this.pendingRender;
		const src = this.getAttribute('src');
		const pageStr = this.getAttribute('page');
		const rect = parseRect(this.getAttribute('rect'));
		if (!src || !pageStr || !rect) return this.showError('missing src/page/rect');
		const page = parseInt(pageStr, 10);
		if (!isFinite(page) || page < 1) return this.showError('invalid page');

		const parentWidth = this.parentElement?.clientWidth ?? 0;
		const containerWidth = this.clientWidth || parentWidth;
		if (containerWidth <= 0) return; // not yet sized

		const cssScale = containerWidth / rect.w;
		const dpr = window.devicePixelRatio || 1;
		const band = scaleBand(cssScale * dpr);

		try {
			const img = await hostGetPageImage(this.host!, src, page, band);
			if (token !== this.pendingRender) return; // stale
			this.currentBand = band;
			// The chunker may report its page dimensions in a coord system
			// that differs from PDF.js's viewport (Chrome's Skia PDFs use a
			// content-scale TextMatrix that makes text positions far larger
			// than MediaBox points). When `page-size` is present, use it —
			// the PDF.js image stretches to fill that area, and all rects
			// (chunk + tag overlays) are in the same coord system.
			const ps = parseRect(`0,0,${this.getAttribute('page-size') ?? '0,0'}`);
			if (ps && ps.w > 0 && ps.h > 0) {
				this.currentPageW = ps.w;
				this.currentPageH = ps.h;
			} else {
				this.currentPageW = img.pageW;
				this.currentPageH = img.pageH;
			}
			this.showImage(img.url, rect, cssScale);
			this.positionTagOverlays(rect, cssScale);
			await this.applyHighlights(src, page);
			this.positionHighlights(rect, cssScale);
			this.clearError();
		} catch (err) {
			if (token !== this.pendingRender) return;
			this.showError(
				`load failed: ${err instanceof Error ? err.message : String(err)}`,
			);
		}
	}

	showImage(url: string, rect: Rect, cssScale: number): void {
		let img = this.imgEl;
		if (!img) {
			img = document.createElement('img');
			img.className = 'pdf-chunk-image';
			this.insertBefore(img, this.firstChild);
			this.imgEl = img;
		}
		img.src = url;
		const pageW = this.currentPageW * cssScale;
		const pageH = this.currentPageH * cssScale;
		img.style.width = `${pageW}px`;
		img.style.height = `${pageH}px`;
		const leftPx = -rect.x * cssScale;
		const topPx = -(this.currentPageH - rect.y - rect.h) * cssScale;
		img.style.left = `${leftPx}px`;
		img.style.top = `${topPx}px`;
		this.style.width = `${rect.w * cssScale}px`;
		this.style.height = `${rect.h * cssScale}px`;
	}

	// Compute match bounding boxes against the PDF's text content and
	// cache them as Rect values in chunker coords. Skips the expensive
	// getTextContent fetch when src, page, and highlight are unchanged.
	async applyHighlights(src: string, page: number): Promise<void> {
		const hlAttr = this.getAttribute('highlight') ?? '';
		const key = `${src}|${page}|${hlAttr}`;
		if (key === this.highlightKey) return;
		this.highlightKey = key;
		this.highlightRects = [];
		if (!hlAttr) return;
		const patterns: RegExp[] = [];
		for (const p of hlAttr.split('\n')) {
			if (!p) continue;
			try {
				patterns.push(new RegExp(p, 'gi'));
			} catch {
				// ignore invalid regex
			}
		}
		if (!patterns.length) return;
		try {
			const doc = await hostGetDocument(this.host!, src);
			const pdfPage = await doc.getPage(page);
			const content = await pdfPage.getTextContent();
			const viewport = pdfPage.getViewport({ scale: 1 });
			const pageH_pdf = viewport.height;
			const pageW_pdf = viewport.width;
			// Scale from PDF points → chunker coords (same stretching the
			// img goes through via currentPageW/H).
			const xScale = this.currentPageW / pageW_pdf;
			const yScale = this.currentPageH / pageH_pdf;
			// Build a flat text string with item offsets so regex matches
			// across item boundaries still produce one bbox per match.
			type ItemInfo = { str: string; x: number; y: number; w: number; h: number };
			const items: ItemInfo[] = [];
			let joined = '';
			const offsets: number[] = []; // offsets[i] = start of item i in `joined`
			for (const it of content.items as unknown as Array<{
				str: string;
				transform: number[];
				width: number;
				height: number;
			}>) {
				const t = it.transform;
				const x = t[4];
				const yBaseline = t[5];
				const fh = Math.abs(t[3]) || it.height || 0;
				const item: ItemInfo = {
					str: it.str,
					x,
					y: yBaseline,
					w: it.width,
					h: fh,
				};
				offsets.push(joined.length);
				joined += item.str + ' ';
				items.push(item);
			}
			const rects: Rect[] = [];
			// subRect approximates the bbox of a substring inside a PDF text
			// item by linear interpolation over the item's width — PDF.js
			// items can be whole lines, so per-item bbox alone highlights
			// the entire line when the match is a single word inside it.
			const subRect = (it: ItemInfo, localStart: number, localEnd: number) => {
				const total = it.str.length;
				if (total <= 0) {
					return { x: it.x, y: it.y, w: it.w, h: it.h };
				}
				const a = Math.max(0, Math.min(total, localStart));
				const b = Math.max(0, Math.min(total, localEnd));
				const lo = Math.min(a, b);
				const hi = Math.max(a, b);
				return {
					x: it.x + (it.w * lo) / total,
					y: it.y,
					w: (it.w * (hi - lo)) / total,
					h: it.h,
				};
			};
			for (const re of patterns) {
				re.lastIndex = 0;
				let m: RegExpExecArray | null;
				while ((m = re.exec(joined)) !== null) {
					if (m[0].length === 0) {
						re.lastIndex++;
						continue;
					}
					const start = m.index;
					const end = m.index + m[0].length;
					let first = -1;
					let last = -1;
					for (let i = 0; i < items.length; i++) {
						const off = offsets[i];
						const itemEnd = off + items[i].str.length;
						if (off < end && itemEnd > start) {
							if (first < 0) first = i;
							last = i;
						}
					}
					if (first < 0) continue;
					let minX = Infinity,
						minY = Infinity,
						maxX = -Infinity,
						maxY = -Infinity;
					for (let i = first; i <= last; i++) {
						const it = items[i];
						const off = offsets[i];
						const localStart = i === first ? start - off : 0;
						const localEnd = i === last ? end - off : it.str.length;
						const sub = subRect(it, localStart, localEnd);
						minX = Math.min(minX, sub.x);
						minY = Math.min(minY, sub.y);
						maxX = Math.max(maxX, sub.x + sub.w);
						maxY = Math.max(maxY, sub.y + sub.h);
					}
					rects.push({
						x: minX * xScale,
						y: minY * yScale,
						w: (maxX - minX) * xScale,
						h: (maxY - minY) * yScale,
					});
				}
			}
			this.highlightRects = rects;
		} catch {
			// getTextContent failure is non-fatal — skip highlights
		}
	}

	positionHighlights(chunkRect: Rect, cssScale: number): void {
		// Reconcile overlay div count with highlightRects.
		while (this.highlightEls.length > this.highlightRects.length) {
			this.highlightEls.pop()?.remove();
		}
		while (this.highlightEls.length < this.highlightRects.length) {
			const el = document.createElement('div');
			el.className = 'pdf-chunk-highlight';
			this.appendChild(el);
			this.highlightEls.push(el);
		}
		for (let i = 0; i < this.highlightRects.length; i++) {
			const r = this.highlightRects[i];
			const el = this.highlightEls[i];
			const leftPx = (r.x - chunkRect.x) * cssScale;
			const topPx = (chunkRect.y + chunkRect.h - r.y - r.h) * cssScale;
			el.style.left = `${leftPx}px`;
			el.style.top = `${topPx}px`;
			el.style.width = `${r.w * cssScale}px`;
			el.style.height = `${r.h * cssScale}px`;
		}
	}

	positionTagOverlays(chunkRect: Rect, cssScale: number): void {
		const tags = this.querySelectorAll<HTMLElement>(':scope > ark-tag[rect]');
		tags.forEach((tag) => {
			const tagRect = parseRect(tag.getAttribute('rect'));
			if (!tagRect) return;
			const leftPx = (tagRect.x - chunkRect.x) * cssScale;
			const topPx = (chunkRect.y + chunkRect.h - tagRect.y - tagRect.h) * cssScale;
			const heightPx = tagRect.h * cssScale;
			tag.style.position = 'absolute';
			tag.style.left = `${leftPx}px`;
			tag.style.top = `${topPx}px`;
			// Single-line tag — no width constraint. With white-space:
			// nowrap the overlay grows to fit the full name+value.
			tag.style.height = `${heightPx}px`;
			tag.style.lineHeight = '1';
			tag.style.padding = '0';
			tag.style.whiteSpace = 'nowrap';
			tag.style.background = 'var(--pdf-tag-bg, var(--pdf-chunk-bg, white))';
			tag.style.fontSize = `${heightPx}px`;
		});
	}

	showError(msg: string): void {
		if (!this.errorEl) {
			this.errorEl = document.createElement('div');
			this.errorEl.className = 'pdf-chunk-error';
			this.errorEl.style.position = 'absolute';
			this.errorEl.style.top = '0';
			this.errorEl.style.right = '0';
			this.errorEl.style.fontSize = '10px';
			this.errorEl.style.padding = '2px 4px';
			this.errorEl.style.background = 'rgba(255, 0, 0, 0.15)';
			this.errorEl.style.color = 'var(--term-error, #c33)';
			this.appendChild(this.errorEl);
		}
		this.errorEl.textContent = msg;
	}

	clearError(): void {
		if (this.errorEl) {
			this.errorEl.remove();
			this.errorEl = null;
		}
	}

	// Slice-and-insert on tag click. Replaces self with three siblings:
	// top <pdf-chunk>, inline <ark-search>, bottom <pdf-chunk>. The
	// original element is held by the close handler's closure (and
	// exposed on the <ark-search> as `pdfOriginalChunk` for devtools)
	// — closing the panel puts it back in place.
	// See seq-pdf-slice.md, R1699-R1702.
	sliceAtTag(tag: HTMLElement, tagRect: Rect, chunkRect: Rect): void {
		const src = this.getAttribute('src')!;
		const page = this.getAttribute('page')!;

		// The tag rect uses the glyph baseline as its bottom edge, so
		// descenders (g, j, p, q, y) hang below rect.y. Push the slice
		// line down by ~25% of the tag's height so the descenders stay
		// in the top slice with the rest of the tag.
		const descentPad = tagRect.h * 0.25;
		const sliceY = tagRect.y - descentPad;
		// Top slice: region from sliceY up to the chunk's top — the
		// clicked tag (including its descenders) stays visible above
		// the search panel.
		const topRect: Rect = {
			x: chunkRect.x,
			y: sliceY,
			w: chunkRect.w,
			h: chunkRect.y + chunkRect.h - sliceY,
		};
		// Bottom slice: region from the chunk's bottom up to sliceY.
		const bottomRect: Rect = {
			x: chunkRect.x,
			y: chunkRect.y,
			w: chunkRect.w,
			h: sliceY - chunkRect.y,
		};

		// Partition tag children into clones for the two slices. Tags
		// at or above sliceY go in the top slice (including the
		// clicked tag itself); tags entirely below go in the bottom.
		// Clones have overlay styles cleared so each slice's
		// positionTagOverlays runs from a clean state.
		const allTags = Array.from(
			this.querySelectorAll<HTMLElement>(':scope > ark-tag[rect]'),
		);
		const topChildren: HTMLElement[] = [];
		const bottomChildren: HTMLElement[] = [];
		for (const t of allTags) {
			const tr = parseRect(t.getAttribute('rect'));
			if (!tr) continue;
			const clone = t.cloneNode(true) as HTMLElement;
			clearOverlayStyles(clone);
			if (tr.y >= sliceY) topChildren.push(clone);
			else if (tr.y + tr.h <= sliceY) bottomChildren.push(clone);
			// Tags intersecting the slice line are dropped.
		}

		const topEl = buildSlice(src, page, topRect, topChildren);
		const bottomEl = buildSlice(src, page, bottomRect, bottomChildren);

		const searchEl = document.createElement('ark-search') as HTMLElement & {
			tag: string;
			value: string;
			api: unknown;
			pdfOriginalChunk: PdfChunkElement;
		};
		searchEl.tag = tag.querySelector('name')?.textContent ?? '';
		searchEl.value = tag.querySelector('value')?.textContent ?? '';
		const api = (document as unknown as { arkSearchAPI?: unknown }).arkSearchAPI;
		if (api) searchEl.api = api;
		searchEl.pdfOriginalChunk = this;
		searchEl.addEventListener(
			'close',
			() => this.mergeSlices(topEl, searchEl, bottomEl),
			{ once: true },
		);

		this.replaceWith(topEl, searchEl, bottomEl);
	}

	mergeSlices(
		topEl: PdfChunkElement,
		searchEl: HTMLElement,
		bottomEl: PdfChunkElement,
	): void {
		if (!topEl.parentElement) return;
		this.querySelectorAll<HTMLElement>(':scope > ark-tag[rect]').forEach(
			clearOverlayStyles,
		);
		topEl.replaceWith(this);
		searchEl.remove();
		bottomEl.remove();
	}
}

function buildSlice(
	src: string,
	page: string,
	rect: Rect,
	children: HTMLElement[],
): PdfChunkElement {
	const el = document.createElement('pdf-chunk') as PdfChunkElement;
	el.setAttribute('src', src);
	el.setAttribute('page', page);
	el.setAttribute('rect', formatRect(rect));
	children.forEach((c) => el.appendChild(c));
	return el;
}

function clearOverlayStyles(tag: HTMLElement): void {
	tag.style.position = '';
	tag.style.left = '';
	tag.style.top = '';
	tag.style.width = '';
	tag.style.height = '';
	tag.style.lineHeight = '';
	tag.style.padding = '';
	tag.style.overflow = '';
	tag.style.background = '';
	tag.style.fontSize = '';
}

if (!customElements.get('pdf-chunk')) {
	customElements.define('pdf-chunk', PdfChunkElement);
}
