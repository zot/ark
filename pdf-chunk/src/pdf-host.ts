// CRC: crc-ArkSearchElement.md (host role) | R1681-R1690, R1746-R1750
// PDF host storage. The host element owns:
//   pdfDocCache         — cached PDFDocumentProxy promises, keyed by src
//   pdfPageCache        — cached baked page-image promises, keyed by src|page|band
//   pdfTextContentCache — cached text-content scans + tag rects, keyed by src|page
//   pdfThemeColors      — sampled theme descriptor from a hidden <ark-tag> probe
//   pdfBlobUrls         — every URL.createObjectURL value, for cleanup
//
// Rendering logic (which requires pdfjs-dist) lives in the pdf-chunk
// element module. The host only provides the caches and the cleanup
// hook — keeping pdfjs out of the ark-search bundle.
//
// All state is element properties (not closure-captured) so devtools
// can inspect and slice-and-insert DOM surgery doesn't misplace the
// ledger.

export interface PageImage {
	url: string;
	pageW: number; // PDF points
	pageH: number; // PDF points
}

// One raw text item from getTextContent(), carried in chunk coords
// (PDF points, origin bottom-left — x,y is the item's baseline start).
export interface TextItem {
	str: string;
	x: number;
	y: number;
	w: number;
	h: number;
}

// One run contributing to a tag — a slice of a TextItem covering
// a byte range of the canonical "@name: value" string.
export interface TextRun {
	x: number;
	y: number;
	w: number;
	h: number;
	start: number;
	end: number;
	text: string;
}

// A tag detected on the page.
export interface TagDescriptor {
	name: string;
	value: string;
	union: { x: number; y: number; w: number; h: number };
	runs: TextRun[];
}

// Cached text-content scan for a page.
export interface PageTextContent {
	items: TextItem[];
	joined: string;
	offsets: number[]; // offsets[i] = start of items[i] in joined
	tags: TagDescriptor[];
	pageW: number;
	pageH: number;
}

// Theme descriptor sampled from a representative <ark-tag>.
export interface ThemeColors {
	name: string;
	value: string;
	punctuation: string;
	fontFamily: string;
	bg: string;
}

export interface PdfHostStorage extends HTMLElement {
	pdfDocCache: Map<string, Promise<unknown>>;
	pdfPageCache: Map<string, Promise<PageImage>>;
	pdfTextContentCache: Map<string, Promise<PageTextContent>>;
	pdfThemeColors: ThemeColors | null;
	pdfBlobUrls: string[];
}

// Snap a rendering scale to a geometric band (factor 1.2 → each band
// covers roughly ±10% around its center). Resizes within a band are
// CSS-only; crossing a band triggers a new render.
export function scaleBand(scale: number): number {
	if (!isFinite(scale) || scale <= 0) return 0;
	return Math.round(Math.log(scale) / Math.log(1.2));
}

export function bandScale(band: number): number {
	return Math.pow(1.2, band);
}

export function pageKey(src: string, page: number, band: number): string {
	return `${src}|${page}|${band}`;
}

export function pageTextKey(src: string, page: number): string {
	return `${src}|${page}`;
}

// Set up host storage on an HTMLElement. Idempotent — safe to call
// repeatedly. Callers are responsible for cleanup at the right
// lifecycle moment (typically disconnectedCallback).
export function setupPdfHost(el: HTMLElement): PdfHostStorage {
	const h = el as PdfHostStorage;
	if (h.pdfDocCache) return h;
	h.pdfDocCache = new Map();
	h.pdfPageCache = new Map();
	h.pdfTextContentCache = new Map();
	h.pdfThemeColors = null;
	h.pdfBlobUrls = [];
	el.setAttribute('data-pdf-host', '');
	registerBeforeUnload();
	return h;
}

// Revoke every blob URL, destroy every cached document, clear the
// maps. Safe to call on a host that's already been cleaned (no-op if
// storage is empty).
export function cleanupPdfHost(host: PdfHostStorage): void {
	for (const url of host.pdfBlobUrls) URL.revokeObjectURL(url);
	host.pdfBlobUrls.length = 0;
	for (const dp of host.pdfDocCache.values()) {
		dp
			.then((doc) => {
				const d = doc as { destroy?: () => void };
				if (typeof d.destroy === 'function') d.destroy();
			})
			.catch(() => {});
	}
	host.pdfDocCache.clear();
	host.pdfPageCache.clear();
	host.pdfTextContentCache.clear();
	host.pdfThemeColors = null;
}

let beforeUnloadRegistered = false;
function registerBeforeUnload(): void {
	if (beforeUnloadRegistered) return;
	beforeUnloadRegistered = true;
	window.addEventListener('beforeunload', () => {
		document.querySelectorAll<HTMLElement>('[data-pdf-host]').forEach((el) => {
			const host = el as Partial<PdfHostStorage>;
			if (host.pdfBlobUrls) cleanupPdfHost(host as PdfHostStorage);
		});
	});
}

// Find the nearest ancestor with host storage installed.
export function findHost(el: HTMLElement): PdfHostStorage | null {
	let cur: HTMLElement | null = el.parentElement;
	while (cur) {
		if ((cur as Partial<PdfHostStorage>).pdfDocCache) return cur as PdfHostStorage;
		cur = cur.parentElement;
	}
	return null;
}
