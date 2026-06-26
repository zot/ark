// CRC: crc-PdfChunkElement.md | Seq: seq-pdf-chunk-render.md, seq-pdf-slice.md
// R1666-R1668, R1677-R1680, R1691-R1702, R1709, R1741-R1757

import * as pdfjsLib from 'pdfjs-dist';
import type { PDFDocumentProxy } from 'pdfjs-dist';
import {
	bandScale,
	findHost,
	pageKey,
	pageTextKey,
	scaleBand,
	setupPdfHost,
	type PageImage,
	type PageTextContent,
	type PdfHostStorage,
	type TagDescriptor,
	type TextItem,
	type TextRun,
	type ThemeColors,
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

// Scoped styles for <pdf-chunk> and its pdf-chunk-context children.
// The hit-region rules (pdf-chunk > ark-tag[data-pdf-hit]) apply ONLY
// when the ark-tag has been placed from PDF.js text-content — R1755.
// Standalone <ark-tag> usage elsewhere (markdown, plain-text) is
// unaffected by anything in this sheet.
const OVERLAY_STYLE_ID = 'pdf-chunk-overlay-styles';
function ensureOverlayStyles(): void {
	if (document.getElementById(OVERLAY_STYLE_ID)) return;
	const s = document.createElement('style');
	s.id = OVERLAY_STYLE_ID;
	s.textContent = `
		pdf-chunk { display: block; position: relative; overflow: hidden; background: var(--pdf-chunk-bg, white); }
		pdf-chunk > img.pdf-chunk-image { position: absolute; pointer-events: none; user-select: none; }
		pdf-chunk > ark-tag[rect] .ark-tag-action { display: none !important; }
		/* Hit-region mode: transparent, pointer-events:none so selection
		   flows through; click dispatch via capture-phase on pdf-chunk. */
		pdf-chunk > ark-tag[data-pdf-hit] {
			position: absolute;
			box-sizing: border-box;
			pointer-events: none;
			color: transparent;
			background: transparent;
			overflow: hidden;
		}
		pdf-chunk > ark-tag[data-pdf-hit] > * { visibility: hidden; }
		/* Fallback overlay (no text-content match) — retains R1694-R1697 styling. */
		pdf-chunk > ark-tag[rect]:not([data-pdf-hit]) {
			box-sizing: border-box;
			cursor: pointer;
		}
		pdf-chunk > .pdf-chunk-highlight {
			position: absolute;
			background: var(--term-accent-dim, rgba(224, 122, 71, 0.35));
			box-shadow: 0 0 0 1px var(--term-accent, #E07A47);
			border-radius: 2px;
			pointer-events: none;
			box-sizing: border-box;
		}
		pdf-chunk > .pdf-text-layer {
			position: absolute;
			inset: 0;
			overflow: hidden;
			opacity: 1;
			line-height: 1;
			text-size-adjust: none;
			forced-color-adjust: none;
			transform-origin: 0 0;
			caret-color: CanvasText;
		}
		pdf-chunk > .pdf-text-layer > span,
		pdf-chunk > .pdf-text-layer > br {
			color: transparent;
			position: absolute;
			white-space: pre;
			cursor: text;
			transform-origin: 0% 0%;
		}
		pdf-chunk > .pdf-text-layer ::selection {
			background: var(--term-accent-dim, rgba(224, 122, 71, 0.35));
			color: transparent;
		}
		pdf-chunk > .pdf-text-layer span.ark-tag::selection {
			background: rgb(from var(--term-accent, rgb(224, 122, 71)) r g b / 0.50);
		}
	`;
	document.head.appendChild(s);
}

// Shared rendering routines — operate on any element that has the
// PdfHostStorage properties. pdfjs-dist is imported only from this
// module, so the ark-search bundle stays pdfjs-free.

const TAG_RE = /@([a-zA-Z][\w.-]*):\s*([^\n]*)/g;

function hostGetDocument(host: PdfHostStorage, src: string): Promise<PDFDocumentProxy> {
	const existing = host.pdfDocCache.get(src) as Promise<PDFDocumentProxy> | undefined;
	if (existing) return existing;
	const fresh = pdfjsLib.getDocument(src).promise;
	host.pdfDocCache.set(src, fresh);
	return fresh;
}

function hostGetPageTextContent(
	host: PdfHostStorage,
	src: string,
	page: number,
): Promise<PageTextContent> {
	const key = pageTextKey(src, page);
	const existing = host.pdfTextContentCache.get(key);
	if (existing) return existing;
	const fresh = fetchPageTextContent(host, src, page);
	host.pdfTextContentCache.set(key, fresh);
	return fresh;
}

async function fetchPageTextContent(
	host: PdfHostStorage,
	src: string,
	page: number,
): Promise<PageTextContent> {
	const doc = await hostGetDocument(host, src);
	const pdfPage = await doc.getPage(page);
	const content = await pdfPage.getTextContent();
	const baseViewport = pdfPage.getViewport({ scale: 1 });
	const items: TextItem[] = [];
	const offsets: number[] = [];
	let joined = '';
	for (const it of content.items as Array<{
		str: string;
		transform: number[];
		width: number;
		height: number;
	}>) {
		const t = it.transform;
		const x = t[4];
		const yBaseline = t[5];
		const fh = Math.abs(t[3]) || it.height || 0;
		offsets.push(joined.length);
		joined += it.str + '\n';
		items.push({ str: it.str, x, y: yBaseline, w: it.width, h: fh });
	}
	const tags = detectTags(items, joined, offsets);
	return { items, joined, offsets, tags, pageW: baseViewport.width, pageH: baseViewport.height };
}

function subRectForItem(
	it: TextItem,
	localStart: number,
	localEnd: number,
): { x: number; y: number; w: number; h: number } {
	const total = it.str.length;
	if (total <= 0) return { x: it.x, y: it.y, w: it.w, h: it.h };
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
}

function detectTags(
	items: TextItem[],
	joined: string,
	offsets: number[],
): TagDescriptor[] {
	const tags: TagDescriptor[] = [];
	TAG_RE.lastIndex = 0;
	let m: RegExpExecArray | null;
	while ((m = TAG_RE.exec(joined)) !== null) {
		if (m[0].length === 0) {
			TAG_RE.lastIndex++;
			continue;
		}
		const name = m[1];
		const rawValue = m[2];
		const trimmedValue = rawValue.replace(/\s+$/, '');
		if (!trimmedValue) continue;
		// Effective match end trims trailing whitespace captured in value.
		const matchStart = m.index;
		const matchEnd = m.index + m[0].length - (rawValue.length - trimmedValue.length);
		const runs: TextRun[] = [];
		let minX = Infinity,
			minY = Infinity,
			maxX = -Infinity,
			maxY = -Infinity;
		for (let i = 0; i < items.length; i++) {
			const off = offsets[i];
			const itemEnd = off + items[i].str.length;
			if (off < matchEnd && itemEnd > matchStart) {
				const it = items[i];
				const localStart = Math.max(0, matchStart - off);
				const localEnd = Math.min(it.str.length, matchEnd - off);
				const sub = subRectForItem(it, localStart, localEnd);
				const runStart = Math.max(0, off - matchStart);
				const runEnd = Math.min(matchEnd - matchStart, itemEnd - matchStart);
				runs.push({
					x: sub.x,
					y: sub.y,
					w: sub.w,
					h: sub.h,
					start: runStart,
					end: runEnd,
					text: it.str.slice(localStart, localEnd),
				});
				minX = Math.min(minX, sub.x);
				minY = Math.min(minY, sub.y);
				maxX = Math.max(maxX, sub.x + sub.w);
				maxY = Math.max(maxY, sub.y + sub.h);
			}
		}
		if (!runs.length) continue;
		tags.push({
			name,
			value: trimmedValue,
			union: { x: minX, y: minY, w: maxX - minX, h: maxY - minY },
			runs,
		});
	}
	return tags;
}

// Parse a tag's `segments` attribute (chunker-supplied exact per-glyph
// bounds for @, name, :, and value lines) into a TagDescriptor with
// runs whose start/end offsets within the canonical match string map
// each run to the correct color under colorForCharInMatch's rules.
// `xScale`/`yScale` convert chunker coordinates to PDF.js text-content
// coordinates (only relevant for Skia-generated PDFs that report
// content in a different scale than MediaBox).
// CRC: crc-PdfChunkElement.md | R1762
function parseSegmentsAttribute(
	segmentsStr: string,
	name: string,
	value: string,
	xScale: number,
	yScale: number,
): TagDescriptor | null {
	const parts = segmentsStr.split('|');
	if (parts.length < 4) return null;
	const rects: Array<{ x: number; y: number; w: number; h: number } | null> = parts.map(
		(p) => {
			const nums = p.split(',').map(Number);
			if (nums.length < 4) return null;
			if (nums.some((v) => !isFinite(v))) return null;
			return {
				x: nums[0] * xScale,
				y: nums[1] * yScale,
				w: nums[2] * xScale,
				h: nums[3] * yScale,
			};
		},
	);
	if (rects.slice(0, 3).some((r) => r === null)) return null;
	const nameLen = name.length;
	const valueLen = value.length;
	const matchEnd = 2 + nameLen + valueLen;
	const runs: TextRun[] = [];
	runs.push({
		x: rects[0]!.x,
		y: rects[0]!.y,
		w: rects[0]!.w,
		h: rects[0]!.h,
		start: 0,
		end: 1,
		text: '@',
	});
	runs.push({
		x: rects[1]!.x,
		y: rects[1]!.y,
		w: rects[1]!.w,
		h: rects[1]!.h,
		start: 1,
		end: 1 + nameLen,
		text: name,
	});
	runs.push({
		x: rects[2]!.x,
		y: rects[2]!.y,
		w: rects[2]!.w,
		h: rects[2]!.h,
		start: 1 + nameLen,
		end: 2 + nameLen,
		text: ':',
	});
	const valueStart = 2 + nameLen;
	for (let i = 3; i < rects.length; i++) {
		const r = rects[i];
		if (!r) continue;
		runs.push({
			x: r.x,
			y: r.y,
			w: r.w,
			h: r.h,
			start: valueStart,
			end: matchEnd,
			text: value,
		});
	}
	let minX = Infinity,
		minY = Infinity,
		maxX = -Infinity,
		maxY = -Infinity;
	for (const r of runs) {
		minX = Math.min(minX, r.x);
		minY = Math.min(minY, r.y);
		maxX = Math.max(maxX, r.x + r.w);
		maxY = Math.max(maxY, r.y + r.h);
	}
	return {
		name,
		value,
		union: { x: minX, y: minY, w: maxX - minX, h: maxY - minY },
		runs,
	};
}

// Walk every <pdf-chunk src=... page=...> in the document and collect
// segment-derived tag descriptors from its <ark-tag segments> children.
// Used at bake time so every tag on the page gets exact per-segment
// recoloring — chunks sharing a page share the scan and the bake.
//
// Chunker segment rects are assumed to be in the same coordinate
// system PDF.js uses for text content (both read from the PDF's
// content stream). The `page-size` attribute describes the chunker's
// page extent for layout, but text positions within the page agree
// between chunker and PDF.js for normal PDFs — no per-coord scaling
// is applied here. Skia-generated PDFs with divergent content-scale
// matrices would need an additional transform; not handled in v1.5.
function collectSegmentDescriptors(src: string, page: number): TagDescriptor[] {
	const out: TagDescriptor[] = [];
	const sel = `pdf-chunk[src="${CSS.escape(src)}"][page="${page}"]`;
	const chunks = document.querySelectorAll<HTMLElement>(sel);
	for (const chunk of Array.from(chunks)) {
		const tags = chunk.querySelectorAll<HTMLElement>(':scope > ark-tag[segments]');
		for (const tag of Array.from(tags)) {
			const segs = tag.getAttribute('segments');
			if (!segs) continue;
			const name = tag.querySelector('name')?.textContent ?? '';
			const value = tag.querySelector('value')?.textContent ?? '';
			if (!name) continue;
			const desc = parseSegmentsAttribute(segs, name, value, 1, 1);
			if (desc) out.push(desc);
		}
	}
	return out;
}

async function getThemeColors(host: PdfHostStorage): Promise<ThemeColors> {
	if (host.pdfThemeColors) return host.pdfThemeColors;
	const probe = document.createElement('ark-tag');
	probe.innerHTML = '<name>sample</name> <value>sample</value>';
	probe.style.position = 'absolute';
	probe.style.left = '-9999px';
	probe.style.top = '0';
	probe.style.visibility = 'hidden';
	document.body.appendChild(probe);
	// Let connectedCallback + styles apply before sampling.
	await new Promise((r) => requestAnimationFrame(() => r(null)));
	const nameEl = probe.querySelector('name');
	const valueEl = probe.querySelector('value');
	const nameStyle = nameEl ? getComputedStyle(nameEl) : getComputedStyle(probe);
	const valueStyle = valueEl ? getComputedStyle(valueEl) : getComputedStyle(probe);
	const puncStyle = nameEl ? getComputedStyle(nameEl, '::before') : nameStyle;
	const root = getComputedStyle(document.documentElement);
	let bg = root.getPropertyValue('--term-bg').trim();
	if (!bg) {
		const bodyBg = getComputedStyle(document.body).backgroundColor;
		bg = bodyBg && bodyBg !== 'rgba(0, 0, 0, 0)' ? bodyBg : '#ffffff';
	}
	const theme: ThemeColors = {
		name: nameStyle.color,
		value: valueStyle.color,
		punctuation: puncStyle.color || nameStyle.color,
		fontFamily: nameStyle.fontFamily || 'monospace',
		bg,
	};
	probe.remove();
	host.pdfThemeColors = theme;
	return theme;
}

// Read the page background color from the rendered canvas by sampling
// corner pixels (PDF margins are almost always empty bg). Falls back
// to the theme bg if sampling fails or returns transparent.
function samplePageBg(
	ctx: CanvasRenderingContext2D,
	canvas: HTMLCanvasElement,
	fallback: string,
): string {
	try {
		const W = canvas.width;
		const H = canvas.height;
		if (W < 4 || H < 4) return fallback;
		const pts: Array<[number, number]> = [
			[2, 2],
			[W - 3, 2],
			[2, H - 3],
			[W - 3, H - 3],
			[Math.floor(W / 2), 2],
		];
		const counts: Record<string, number> = {};
		let best = '';
		let bestCount = 0;
		for (const [x, y] of pts) {
			const d = ctx.getImageData(x, y, 1, 1).data;
			if (d[3] === 0) continue; // transparent — skip
			const key = `rgb(${d[0]},${d[1]},${d[2]})`;
			counts[key] = (counts[key] ?? 0) + 1;
			if (counts[key] > bestCount) {
				bestCount = counts[key];
				best = key;
			}
		}
		return best || fallback;
	} catch {
		return fallback;
	}
}

// Parse a CSS color like "rgb(r, g, b)" or "#rrggbb" into [r, g, b].
// Input comes from getComputedStyle so it's normalized to rgb() form.
function parseRgb(s: string): [number, number, number] {
	const m = /^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/.exec(s);
	if (m) return [parseInt(m[1], 10), parseInt(m[2], 10), parseInt(m[3], 10)];
	const h = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(s);
	if (h) return [parseInt(h[1], 16), parseInt(h[2], 16), parseInt(h[3], 16)];
	return [0, 0, 0];
}

// Recolor the PDF's own rasterized tag text in place. PDF.js has
// already drawn the text in its native fonts and positions; we walk
// each tag rect's pixels, detect text-like pixels by luminance
// distance from the page background, and lerp from bg toward the
// target theme color by that distance. Glyph shapes and metrics are
// the PDF's; only the ink color changes.
function recolorTagsInCanvas(
	ctx: CanvasRenderingContext2D,
	canvas: HTMLCanvasElement,
	tags: TagDescriptor[],
	theme: ThemeColors,
	bs: number,
	pageH: number,
	pageBg: string,
): void {
	const [bgR, bgG, bgB] = parseRgb(pageBg);
	const bgLum = 0.2126 * bgR + 0.7152 * bgG + 0.0722 * bgB;
	if (bgLum < 1) return; // avoid divide-by-zero on black bg
	const colorCache: Record<string, [number, number, number]> = {};
	const getColor = (s: string) => (colorCache[s] ||= parseRgb(s));
	const nameCol = getColor(theme.name);
	const valueCol = getColor(theme.value);
	const puncCol = getColor(theme.punctuation);
	// Glow is a blurred copy of the text drawn in the theme's bg
	// color. On a light PDF page the dark theme bg produces a visible
	// halo behind each glyph; on a dark-theme-matching page it fades
	// into the surroundings. The halo is the ark UI's background
	// color — the tag "reads like ark" regardless of what the PDF's
	// own page color is.
	const [glowR, glowG, glowB] = parseRgb(theme.bg);
	const canvasW = canvas.width;
	const canvasH = canvas.height;
	// Chunker BBoxes are baseline-aligned — glyphs with descenders
	// (@, p, q, j, g, y) extend below the reported rect. Pad each run
	// downward so those pixels fall inside the recolor scope. The pad
	// is clamped by the gap to the line below so it doesn't overwrite
	// the top of the next tag's glyphs.
	const DESCENDER_PAD = 0.4;
	const ASCENDER_PAD = 0.3;
	const orderedTags = [...tags].sort((a, b) => b.union.y - a.union.y);
	// Phase 1: compute geometry + per-run boxes + pristine snapshots
	// for every tag before any writes. Edge pads of one tag extend
	// into neighbors' regions; snapshotting up-front keeps every
	// detection pass reading PDF-native pixels.
	type Prepared = {
		tag: TagDescriptor;
		nameLen: number;
		x0: number;
		y0: number;
		w: number;
		h: number;
		blurPx: number;
		snap: ImageData;
		runBoxes: Array<{
			x0: number;
			y0: number;
			x1: number;
			y1: number;
			start: number;
			end: number;
			widths: number[];
			totalWidth: number;
		}>;
	};
	const prepared: Prepared[] = [];
	const savedFont = ctx.font;
	for (let ti = 0; ti < orderedTags.length; ti++) {
		const tag = orderedTags[ti];
		// Descender pad (canvas-down, PDF-down): use the full desired
		// amount — descenders typically extend 30-40% of glyph height
		// below baseline, and the tag-below's ascender clamp keeps the
		// two from colliding on their glyphs. The descender halo of
		// the tag above will bleed faintly into the line below's top
		// through source-over composite, which is acceptable given
		// bottom-up processing (upper tags draw last and win overlaps).
		const descenderPdf = tag.union.h * DESCENDER_PAD;
		// Ascender pad (canvas-up, PDF-up): clamp by gap to line above.
		// pdftext BBoxes don't include ascender space; without this the
		// tops of k/t/l/f and the `@` tail register as "outside all
		// runs" so they don't get recolored or glowed.
		let ascenderPdf = tag.union.h * ASCENDER_PAD;
		for (let tj = ti - 1; tj >= 0; tj--) {
			const above = orderedTags[tj];
			const xOverlap =
				above.union.x < tag.union.x + tag.union.w &&
				above.union.x + above.union.w > tag.union.x;
			if (!xOverlap) continue;
			const gapPdf = above.union.y - (tag.union.y + tag.union.h);
			if (gapPdf < ascenderPdf) {
				ascenderPdf = Math.max(0, gapPdf - 0.5);
			}
			break;
		}
		const descenderPx = descenderPdf * bs;
		const ascenderPx = ascenderPdf * bs;
		const blurPx = Math.max(2, tag.union.h * bs * 0.25);
		const edgePad = Math.ceil(blurPx * 3);
		// Region extends past ascender/descender by the blur radius so
		// the blurred bg-fill has room to fade smoothly. Since the
		// combined buffer is transparent outside glyph shapes, drawing
		// it onto main via source-over won't overwrite neighboring
		// tags' content — only the text-shape alpha gets composited.
		const unionX = tag.union.x * bs - edgePad;
		const unionY = (pageH - tag.union.y - tag.union.h) * bs - ascenderPx - blurPx * 2;
		const unionW = tag.union.w * bs + 2 * edgePad;
		const unionH = tag.union.h * bs + descenderPx + ascenderPx + 4 * blurPx;
		const x0 = Math.max(0, Math.floor(unionX));
		const y0 = Math.max(0, Math.floor(unionY));
		const x1 = Math.min(canvasW, Math.ceil(unionX + unionW));
		const y1 = Math.min(canvasH, Math.ceil(unionY + unionH));
		const w = x1 - x0;
		const h = y1 - y0;
		if (w <= 0 || h <= 0) continue;
		const runBoxes = tag.runs.map((r) => {
			const rx0 = r.x * bs;
			const ry0 = (pageH - r.y - r.h) * bs;
			const fontSize = r.h * bs;
			const ascender = fontSize * ASCENDER_PAD;
			const descender = fontSize * DESCENDER_PAD;
			// Clamp pads to the shared per-tag budgets so the run box
			// doesn't overhang into neighboring lines.
			const effAscender = Math.min(ascender, ascenderPx);
			const effDescender = Math.min(descender, descenderPx);
			ctx.font = `${fontSize}px Arial, sans-serif`;
			const widths: number[] = [0];
			let acc = 0;
			for (let i = 0; i < r.text.length; i++) {
				acc += ctx.measureText(r.text[i]).width;
				widths.push(acc);
			}
			return {
				x0: rx0,
				y0: ry0 - effAscender,
				x1: rx0 + r.w * bs,
				y1: ry0 + r.h * bs + effDescender,
				start: r.start,
				end: r.end,
				widths,
				totalWidth: acc,
			};
		});
		const snap = ctx.getImageData(x0, y0, w, h);
		prepared.push({
			tag,
			nameLen: tag.name.length,
			x0,
			y0,
			w,
			h,
			blurPx,
			snap,
			runBoxes,
		});
	}
	ctx.font = savedFont;
	// Phase 2: five-step solid-background compositing. For each tag:
	// 1. Silhouette tile (black, alpha = textness)
	// 2. Expansion blur (spreads glyph shape outward)
	// 3. Threshold to solid bg (alpha > T → opaque theme.bg)
	// 4. Small edge blur (softens the cutout boundary)
	// 5. Recolored text tile on top
	const ALPHA_THRESHOLD = 8;
	for (let pi = prepared.length - 1; pi >= 0; pi--) {
		const p = prepared[pi];
		const { nameLen, x0, y0, w, h, blurPx, snap, runBoxes } = p;
		const mkCtx = (): CanvasRenderingContext2D | null => {
			const c = document.createElement('canvas');
			c.width = w;
			c.height = h;
			return c.getContext('2d');
		};
		const silCtx = mkCtx();
		const txtCtx = mkCtx();
		if (!silCtx || !txtCtx) continue;
		const silData = silCtx.createImageData(w, h);
		const txtData = txtCtx.createImageData(w, h);
		for (let py = 0; py < h; py++) {
			const canvasY = y0 + py + 0.5;
			for (let px = 0; px < w; px++) {
				const canvasX = x0 + px + 0.5;
				const idx = (py * w + px) * 4;
				const pr = snap.data[idx];
				const pg = snap.data[idx + 1];
				const pb = snap.data[idx + 2];
				const pLum = 0.2126 * pr + 0.7152 * pg + 0.0722 * pb;
				const textness = Math.max(0, Math.min(1, 1 - pLum / bgLum));
				if (textness < 0.05) continue;
				let box = null;
				for (const b of runBoxes) {
					if (
						canvasX >= b.x0 &&
						canvasX < b.x1 &&
						canvasY >= b.y0 &&
						canvasY < b.y1
					) {
						box = b;
						break;
					}
				}
				if (!box) continue;
				const alpha = Math.min(255, Math.round(textness * 255));
				silData.data[idx + 3] = alpha;
				const ratio = (canvasX - box.x0) / Math.max(1, box.x1 - box.x0);
				const measuredPos = ratio * box.totalWidth;
				let local = 0;
				while (
					local + 1 < box.widths.length &&
					box.widths[local + 1] <= measuredPos
				) {
					local++;
				}
				const charIdx = Math.min(box.end - 1, box.start + local);
				let target: [number, number, number];
				if (charIdx === 0) target = puncCol;
				else if (charIdx <= nameLen) target = nameCol;
				else if (charIdx === nameLen + 1) target = puncCol;
				else target = valueCol;
				txtData.data[idx] = target[0];
				txtData.data[idx + 1] = target[1];
				txtData.data[idx + 2] = target[2];
				txtData.data[idx + 3] = alpha;
			}
		}
		// Step 1 → 2: put silhouette, blur to expand shape
		silCtx.putImageData(silData, 0, 0);
		const expandCtx = mkCtx();
		if (!expandCtx) continue;
		expandCtx.filter = `blur(${blurPx}px)`;
		expandCtx.drawImage(silCtx.canvas, 0, 0);
		// Step 3: threshold — any alpha above threshold → solid bg
		const expanded = expandCtx.getImageData(0, 0, w, h);
		for (let i = 3; i < expanded.data.length; i += 4) {
			if (expanded.data[i] > ALPHA_THRESHOLD) {
				expanded.data[i - 3] = glowR;
				expanded.data[i - 2] = glowG;
				expanded.data[i - 1] = glowB;
				expanded.data[i] = 255;
			} else {
				expanded.data[i] = 0;
			}
		}
		expandCtx.putImageData(expanded, 0, 0);
		// Step 4: small edge blur to soften the cutout boundary
		const edgeBlurPx = Math.max(1, blurPx * 0.3);
		const combCtx = mkCtx();
		if (!combCtx) continue;
		combCtx.filter = `blur(${edgeBlurPx}px)`;
		combCtx.drawImage(expandCtx.canvas, 0, 0);
		// Step 5: draw recolored text on top of the softened bg
		combCtx.filter = 'none';
		txtCtx.putImageData(txtData, 0, 0);
		combCtx.drawImage(txtCtx.canvas, 0, 0);
		// Clip to generous rect around runBox union
		let clipX0 = Infinity, clipY0 = Infinity, clipX1 = -Infinity, clipY1 = -Infinity;
		for (const b of runBoxes) {
			clipX0 = Math.min(clipX0, b.x0);
			clipY0 = Math.min(clipY0, b.y0);
			clipX1 = Math.max(clipX1, b.x1);
			clipY1 = Math.max(clipY1, b.y1);
		}
		ctx.save();
		ctx.beginPath();
		ctx.rect(clipX0 - blurPx, clipY0, clipX1 - clipX0 + 2 * blurPx, clipY1 - clipY0);
		ctx.clip();
		ctx.drawImage(combCtx.canvas, x0, y0);
		ctx.restore();
	}
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
	// Resolve document, text content, and theme in parallel where possible.
	const [doc, textContent, theme] = await Promise.all([
		hostGetDocument(host, src),
		hostGetPageTextContent(host, src, page),
		getThemeColors(host),
	]);
	const pdfPage = await doc.getPage(page);
	const scale = bandScale(band);
	const viewport = pdfPage.getViewport({ scale });
	const canvas = document.createElement('canvas');
	canvas.width = Math.ceil(viewport.width);
	canvas.height = Math.ceil(viewport.height);
	const ctx = canvas.getContext('2d');
	if (!ctx) throw new Error('no 2d canvas context');
	await pdfPage.render({ canvasContext: ctx, viewport }).promise;
	// PDF.js has rendered the page at true PDF fidelity — including the
	// tag text in its native fonts. Sample the page background from a
	// margin pixel, then walk each tag rect's pixels and recolor text
	// pixels toward the theme colors by their luminance distance from
	// bg. Letterforms, metrics, and antialiasing all come from the
	// PDF; only the ink color changes.
	// Prefer chunker-supplied segments (exact per-glyph bounds for @,
	// name, :, value) over PDF.js's approximate detection.
	const segmentTags = collectSegmentDescriptors(src, page);
	const bakeTags = segmentTags.length > 0 ? segmentTags : textContent.tags;
	const pageBg = samplePageBg(ctx, canvas, theme.bg);
	recolorTagsInCanvas(ctx, canvas, bakeTags, theme, scale, textContent.pageH, pageBg);
	const blob = await new Promise<Blob | null>((resolve) =>
		canvas.toBlob(resolve, 'image/png'),
	);
	if (!blob) throw new Error('canvas.toBlob failed');
	const url = URL.createObjectURL(blob);
	host.pdfBlobUrls.push(url);
	return { url, pageW: textContent.pageW, pageH: textContent.pageH };
}

export class PdfChunkElement extends HTMLElement {
	// Element properties — not closure-captured, inspectable in devtools,
	// survive DOM surgery (slice-and-insert).
	host: PdfHostStorage | null = null;
	imgEl: HTMLImageElement | null = null;
	textLayerEl: HTMLDivElement | null = null;
	errorEl: HTMLDivElement | null = null;
	currentBand: number | null = null;
	currentPageW = 0; // chunk-space page width
	currentPageH = 0; // chunk-space page height
	resizeObserver: ResizeObserver | null = null;
	pendingRender = 0; // monotonic token — cancels stale renders
	overlayClickBound = false;
	// Cached match rects (in chunk coords) from the last text-content
	// scan. Recomputed when src/page/highlight changes; re-placed (CSS-only)
	// on band-free resizes.
	highlightRects: Rect[] = [];
	highlightEls: HTMLDivElement[] = [];
	highlightKey = ''; // `${src}|${page}|${highlightAttr}` — skip recompute on identical

	static get observedAttributes(): string[] {
		return ['src', 'page', 'rect', 'page-size', 'highlight', 'zoom'];
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

	// Capture-phase click handler: rect-tests against positioned <ark-tag>
	// children so selection can flow through (`pointer-events: none` on
	// hit regions). On a hit, run slice-and-insert; on a miss, pass
	// through so the PDF.js text layer can handle selection. (R1755)
	handleClick(ev: MouseEvent): void {
		const chunkRect = parseRect(this.getAttribute('rect'));
		if (!chunkRect) return;
		const bounds = this.getBoundingClientRect();
		const cx = ev.clientX - bounds.left;
		const cy = ev.clientY - bounds.top;
		const children = this.querySelectorAll<HTMLElement>(':scope > ark-tag[rect]');
		for (const child of Array.from(children)) {
			const bb = child.getBoundingClientRect();
			if (bb.width <= 0 || bb.height <= 0) continue;
			const bx = bb.left - bounds.left;
			const by = bb.top - bounds.top;
			if (cx < bx || cx > bx + bb.width) continue;
			if (cy < by || cy > by + bb.height) continue;
			const tagRect = parseRect(child.getAttribute('rect'));
			if (!tagRect) continue;
			ev.preventDefault();
			ev.stopImmediatePropagation();
			const nameEl = child.querySelector('name');
			const valueEl = child.querySelector('value');
			child.dispatchEvent(
				new CustomEvent('ark-tag-click', {
					bubbles: true,
					detail: {
						name: nameEl?.textContent ?? '',
						value: valueEl?.textContent ?? '',
					},
				}),
			);
			this.sliceAtTag(child, tagRect, chunkRect);
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

		const zoomAttr = parseFloat(this.getAttribute('zoom') ?? '');
		const explicitZoom = isFinite(zoomAttr) && zoomAttr > 0;
		const parentWidth = this.parentElement?.clientWidth ?? 0;
		const containerWidth = this.clientWidth || parentWidth;
		if (!explicitZoom && containerWidth <= 0) return;

		const cssScale = explicitZoom ? zoomAttr : containerWidth / rect.w;
		const dpr = window.devicePixelRatio || 1;
		const band = scaleBand(cssScale * dpr);

		try {
			const [img, textContent] = await Promise.all([
				hostGetPageImage(this.host!, src, page, band),
				hostGetPageTextContent(this.host!, src, page),
			]);
			if (token !== this.pendingRender) return; // stale
			this.currentBand = band;
			// The chunker's page-size is the observed content bounding
			// box, which can be smaller than the PDF's MediaBox. The
			// image is rendered by PDF.js at the full MediaBox, so
			// coordinates must use those dimensions for correct offsets.
			// Exception: Skia-generated PDFs report page-size LARGER
			// than the MediaBox (text positions are scaled up); in that
			// case page-size is authoritative.
			const ps = parseRect(`0,0,${this.getAttribute('page-size') ?? '0,0'}`);
			if (ps && ps.w > 0 && ps.h > 0 &&
				(ps.w > textContent.pageW || ps.h > textContent.pageH)) {
				this.currentPageW = ps.w;
				this.currentPageH = ps.h;
			} else {
				this.currentPageW = textContent.pageW;
				this.currentPageH = textContent.pageH;
			}
			const xScale = this.currentPageW / textContent.pageW;
			const yScale = this.currentPageH / textContent.pageH;
			this.showImage(img.url, rect, cssScale);
			this.positionHitRegions(rect, cssScale, textContent.tags, xScale, yScale);
			this.positionRegions(rect, cssScale);
			this.mountTextLayer(rect, cssScale, textContent, xScale, yScale);
			this.applyHighlightsFromSpans();
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

	applyHighlightsFromSpans(): void {
		const hlAttr = this.getAttribute('highlight') ?? '';
		const key = `${this.getAttribute('src')}|${this.getAttribute('page')}|${hlAttr}`;
		if (key === this.highlightKey) return;
		this.highlightKey = key;
		while (this.highlightEls.length) this.highlightEls.pop()?.remove();
		this.highlightRects = [];
		if (!hlAttr || !this.textLayerEl) return;
		const patterns: RegExp[] = [];
		for (const p of hlAttr.split('\n')) {
			if (!p) continue;
			try { patterns.push(new RegExp(p, 'gi')); } catch { /* skip */ }
		}
		if (!patterns.length) return;
		const spans = Array.from(this.textLayerEl.querySelectorAll('span'));
		if (!spans.length) return;
		const offsets: number[] = [];
		let joined = '';
		for (const sp of spans) {
			offsets.push(joined.length);
			joined += sp.textContent ?? '';
		}
		const elRect = this.getBoundingClientRect();
		for (const re of patterns) {
			re.lastIndex = 0;
			let m: RegExpExecArray | null;
			while ((m = re.exec(joined)) !== null) {
				if (m[0].length === 0) { re.lastIndex++; continue; }
				const start = m.index;
				const end = start + m[0].length;
				let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
				const range = document.createRange();
				for (let i = 0; i < spans.length; i++) {
					const off = offsets[i];
					const text = spans[i].textContent ?? '';
					const itemEnd = off + text.length;
					if (off >= end || itemEnd <= start) continue;
					const node = spans[i].firstChild;
					if (!node) continue;
					const localStart = Math.max(0, start - off);
					const localEnd = Math.min(text.length, end - off);
					range.setStart(node, localStart);
					range.setEnd(node, localEnd);
					const r = range.getBoundingClientRect();
					if (r.width === 0 && r.height === 0) continue;
					minX = Math.min(minX, r.left - elRect.left);
					minY = Math.min(minY, r.top - elRect.top);
					maxX = Math.max(maxX, r.right - elRect.left);
					maxY = Math.max(maxY, r.bottom - elRect.top);
				}
				if (minX === Infinity) continue;
				const el = document.createElement('div');
				el.className = 'pdf-chunk-highlight';
				el.style.left = `${minX}px`;
				el.style.top = `${minY}px`;
				el.style.width = `${maxX - minX}px`;
				el.style.height = `${maxY - minY}px`;
				this.appendChild(el);
				this.highlightEls.push(el);
				this.highlightRects.push({ x: minX, y: minY, w: maxX - minX, h: maxY - minY });
			}
		}
	}

	// Match each <ark-tag> child against the detected tag descriptors
	// (by normalized name/value + proximity) and position as a
	// transparent hit region (R1755). Children that fail to match fall
	// back to the chunker-rect visible overlay (R1745).
	positionHitRegions(
		chunkRect: Rect,
		cssScale: number,
		tags: TagDescriptor[],
		xScale: number,
		yScale: number,
	): void {
		// Slack in chunk-coord PDF points: one line height on each side
		// plus a small horizontal pad (R1743).
		const slackY = this.estimateLineHeight(tags, yScale);
		const slackX = Math.max(8, chunkRect.w * 0.05);
		const region = {
			x: chunkRect.x - slackX,
			y: chunkRect.y - slackY,
			w: chunkRect.w + 2 * slackX,
			h: chunkRect.h + 2 * slackY,
		};
		const children = Array.from(
			this.querySelectorAll<HTMLElement>(':scope > ark-tag[rect]'),
		);
		const claimed = new Set<TagDescriptor>();
		for (const child of children) {
			const tagRect = parseRect(child.getAttribute('rect'));
			if (!tagRect) {
				clearOverlayStyles(child);
				continue;
			}
			const childName = (child.querySelector('name')?.textContent ?? '').normalize('NFKC');
			const childValue = (child.querySelector('value')?.textContent ?? '').normalize('NFKC');
			// Find nearest matching descriptor inside the expanded chunk region.
			let best: TagDescriptor | null = null;
			let bestDist = Infinity;
			const cx = tagRect.x + tagRect.w / 2;
			const cy = tagRect.y + tagRect.h / 2;
			for (const t of tags) {
				if (claimed.has(t)) continue;
				if (t.name.normalize('NFKC') !== childName) continue;
				if (t.value.normalize('NFKC') !== childValue) continue;
				// Translate descriptor center into chunk coords before
				// proximity-testing against the child's chunker rect.
				const tcx = (t.union.x + t.union.w / 2) * xScale;
				const tcy = (t.union.y + t.union.h / 2) * yScale;
				if (tcx < region.x || tcx > region.x + region.w) continue;
				if (tcy < region.y || tcy > region.y + region.h) continue;
				const dx = tcx - cx;
				const dy = tcy - cy;
				const d2 = dx * dx + dy * dy;
				if (d2 < bestDist) {
					bestDist = d2;
					best = t;
				}
			}
			if (best) {
				claimed.add(best);
				(child as HTMLElement & { textRuns?: TextRun[] }).textRuns = best.runs;
				child.setAttribute('data-pdf-hit', '');
				const unionX = best.union.x * xScale;
				const unionY = best.union.y * yScale;
				const unionW = best.union.w * xScale;
				const unionH = best.union.h * yScale;
				const leftPx = (unionX - chunkRect.x) * cssScale;
				const topPx = (chunkRect.y + chunkRect.h - unionY - unionH) * cssScale;
				child.style.left = `${leftPx}px`;
				child.style.top = `${topPx}px`;
				child.style.width = `${unionW * cssScale}px`;
				child.style.height = `${unionH * cssScale}px`;
				// Other visible-overlay styles are cleared (CSS handles
				// transparent-hit-region defaults via [data-pdf-hit]).
				child.style.background = '';
				child.style.fontSize = '';
				child.style.lineHeight = '';
				child.style.padding = '';
				child.style.whiteSpace = '';
			} else {
				// Fallback: visible overlay at chunker rect (R1745).
				child.removeAttribute('data-pdf-hit');
				const leftPx = (tagRect.x - chunkRect.x) * cssScale;
				const topPx = (chunkRect.y + chunkRect.h - tagRect.y - tagRect.h) * cssScale;
				const heightPx = tagRect.h * cssScale;
				child.style.position = 'absolute';
				child.style.left = `${leftPx}px`;
				child.style.top = `${topPx}px`;
				child.style.width = '';
				child.style.height = `${heightPx}px`;
				child.style.lineHeight = '1';
				child.style.padding = '0';
				child.style.whiteSpace = 'nowrap';
				child.style.background = 'var(--pdf-tag-bg, var(--pdf-chunk-bg, white))';
				child.style.fontSize = `${heightPx}px`;
			}
		}
	}

	// R2422: positionRegions — position <ark-curate-region rect="...">
	// children absolutely above the canvas. Same coordinate transform as
	// positionHitRegions' fallback path, applied to the curate-pin overlay
	// regions emitted by the server's renderPdfChunksByPage.
	positionRegions(chunkRect: Rect, cssScale: number): void {
		const regions = Array.from(
			this.querySelectorAll<HTMLElement>(':scope > ark-curate-region[rect]'),
		);
		for (const region of regions) {
			const r = parseRect(region.getAttribute('rect'));
			if (!r) {
				clearOverlayStyles(region);
				continue;
			}
			const leftPx = (r.x - chunkRect.x) * cssScale;
			const topPx = (chunkRect.y + chunkRect.h - r.y - r.h) * cssScale;
			region.style.position = 'absolute';
			region.style.left = `${leftPx}px`;
			region.style.top = `${topPx}px`;
			region.style.width = `${r.w * cssScale}px`;
			region.style.height = `${r.h * cssScale}px`;
		}
	}

	// Average tag-run height in chunk-coord units, fallback to rect.h
	// heuristic when no tags were detected. Used to size the slack
	// margin around positionHitRegions' match scope (R1743).
	estimateLineHeight(tags: TagDescriptor[], yScale: number): number {
		let sum = 0;
		let count = 0;
		for (const t of tags) {
			for (const r of t.runs) {
				if (r.h > 0) {
					sum += r.h;
					count++;
				}
			}
		}
		if (count > 0) return (sum / count) * yScale * 1.5;
		// Fallback: a small constant in chunk coords.
		return 16;
	}

	// PDF.js-style text layer: one absolutely-positioned transparent
	// span per text item for browser text selection. Consumes the cached
	// getTextContent result (R1756). Restricted to items inside the
	// chunk rect so we don't inflate the DOM for other regions of the
	// page.
	mountTextLayer(
		chunkRect: Rect,
		cssScale: number,
		content: PageTextContent,
		xScale: number,
		yScale: number,
	): void {
		let layer = this.textLayerEl;
		if (!layer) {
			layer = document.createElement('div');
			layer.className = 'pdf-text-layer';
			this.appendChild(layer);
			this.textLayerEl = layer;
		}
		layer.textContent = '';
		const mc = document.createElement('canvas').getContext('2d');
		if (!mc) return;
		const items = content.items;
		const tagItems = new Set<number>();
		for (let ii = 0; ii < items.length; ii++) {
			const it = items[ii];
			for (const t of content.tags) {
				if (it.x + it.w > t.union.x && it.x < t.union.x + t.union.w &&
					it.y + it.h > t.union.y && it.y < t.union.y + t.union.h) {
					tagItems.add(ii);
					break;
				}
			}
		}
		for (let ii = 0; ii < items.length; ii++) {
			const it = items[ii];
			if (!it.str || !it.str.trim()) continue;
			const x = it.x * xScale;
			const y = it.y * yScale;
			const w = it.w * xScale;
			const h = it.h * yScale;
			if (x + w < chunkRect.x || x > chunkRect.x + chunkRect.w) continue;
			if (y + h < chunkRect.y || y > chunkRect.y + chunkRect.h) continue;
			const itemLeftPx = (x - chunkRect.x) * cssScale;
			const topPx = (chunkRect.y + chunkRect.h - y - h) * cssScale;
			const fontSize = h * cssScale;
			const targetW = w * cssScale;
			mc.font = `${fontSize}px sans-serif`;
			const totalMeasured = mc.measureText(it.str).width;
			if (totalMeasured <= 0 || targetW <= 0) continue;
			const pdfScale = targetW / totalMeasured;
			const parts = it.str.match(/[^\s-]+|-\s*|\s+/g) || [it.str];
			let offset = 0;
			const isTag = tagItems.has(ii);
			for (const part of parts) {
				const partMeasured = mc.measureText(part).width;
				const span = document.createElement('span');
				span.textContent = part;
				if (isTag) span.className = 'ark-tag';
				span.style.left = `${itemLeftPx + offset * pdfScale}px`;
				span.style.top = `${topPx}px`;
				span.style.fontSize = `${fontSize}px`;
				layer.appendChild(span);
				const naturalW = span.getBoundingClientRect().width;
				if (naturalW > 0) {
					span.style.transform = `scaleX(${partMeasured * pdfScale / naturalW})`;
				}
				offset += partMeasured;
			}
			// Bridge gap to next item on the same line with a space
			// so selection across font changes stays continuous.
			const next = ii + 1 < items.length ? items[ii + 1] : null;
			if (next && next.str && Math.abs(next.y * yScale - y) < h * 0.5) {
				const gapLeft = itemLeftPx + targetW;
				const nextLeftPx = (next.x * xScale - chunkRect.x) * cssScale;
				const gapW = nextLeftPx - gapLeft;
				const spaceW = mc.measureText(' ').width * pdfScale;
				if (gapW > spaceW * 0.4) {
					const sp = document.createElement('span');
					sp.textContent = ' ';
					sp.style.left = `${gapLeft}px`;
					sp.style.top = `${topPx}px`;
					sp.style.fontSize = `${fontSize}px`;
					sp.style.width = `${gapW}px`;
					sp.style.display = 'inline-block';
					sp.style.transform = 'scaleX(0.01)';
					layer.appendChild(sp);
				}
			}
		}
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
		const pageSize = this.getAttribute('page-size');

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

		const topEl = buildSlice(src, page, topRect, topChildren, pageSize);
		const bottomEl = buildSlice(src, page, bottomRect, bottomChildren, pageSize);

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
	pageSize: string | null,
): PdfChunkElement {
	const el = document.createElement('pdf-chunk') as PdfChunkElement;
	el.setAttribute('src', src);
	el.setAttribute('page', page);
	el.setAttribute('rect', formatRect(rect));
	if (pageSize) el.setAttribute('page-size', pageSize);
	children.forEach((c) => el.appendChild(c));
	return el;
}

function clearOverlayStyles(tag: HTMLElement): void {
	tag.removeAttribute('data-pdf-hit');
	tag.style.position = '';
	tag.style.left = '';
	tag.style.top = '';
	tag.style.width = '';
	tag.style.height = '';
	tag.style.lineHeight = '';
	tag.style.padding = '';
	tag.style.overflow = '';
	tag.style.whiteSpace = '';
	tag.style.background = '';
	tag.style.fontSize = '';
}

if (!customElements.get('pdf-chunk')) {
	customElements.define('pdf-chunk', PdfChunkElement);
}
