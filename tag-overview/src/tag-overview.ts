// CRC: crc-ArkExtTagsElement.md, crc-TagOverviewSidebar.md
// Seq: seq-tag-overview-load.md, seq-tag-overview-click.md
//
// Tag overview frontend. One bundle defines the body indicator
// custom element <ark-ext-tags> and bootstraps the right-side
// sidebar (TagOverviewSidebar). Stage A: ext-tags dropdown +
// sidebar with badge, three modes, entries, click-to-scroll,
// search dispatch, navigate to source.
//
// Deferred to Stage B: substring + category filter, resize handle,
// I-record width persistence, auto-track on scroll, abbreviated-mode
// peek, hover tooltip on the ↗ icon, ARIA polish.

// ---- Shared types ---------------------------------------------------

type EntryKind = "heading" | "inline" | "ext";

interface Entry {
  kind: EntryKind;
  elementId: string;          // id="ark-tag-N" or id="ark-heading-N"
  // Heading
  headingText?: string;
  headingLevel?: number;      // 1-6 (markdown), 0 for <ark-heading> (PDF, flat)
  // Tag/ext
  tagName?: string;
  tagValue?: string;
  // Ext only
  externalFile?: string;
  externalTarget?: string;
}

interface Section {
  // First entry of the section — heading or tag — anchors the section.
  anchor: Entry;
  entries: Entry[];           // includes anchor if anchor itself is a tag/ext
}

type Mode = "collapsed" | "abbreviated" | "full";
const MODE_CYCLE: Record<Mode, Mode> = {
  collapsed: "abbreviated",
  abbreviated: "full",
  full: "collapsed",
};
const MODE_GLYPH: Record<Mode, string> = {
  collapsed: "▢",
  abbreviated: "▤",
  full: "▦",
};

// ---- <ark-ext-tags> -------------------------------------------------
// R2065-R2072, R2081, R2082

const EXT_TAGS_TAG = "ark-ext-tags";

class ArkExtTagsElement extends HTMLElement {
  private indicator: HTMLElement | null = null;
  private dropdown: HTMLElement | null = null;
  private static activeOwner: ArkExtTagsElement | null = null;

  connectedCallback(): void {
    if (this.indicator) return;
    const childCount = this.querySelectorAll(":scope > ark-tag").length;
    this.indicator = this.buildIndicator(childCount > 1);
    this.appendChild(this.indicator);
    // PDF positioning of the indicator over the <pdf-chunk> canvas
    // via the rect attribute is deferred to Stage C — needs
    // coordination with <pdf-chunk>'s rendered coordinate space.
  }

  private buildIndicator(stacked: boolean): HTMLElement {
    const span = document.createElement("span");
    span.className = "ark-ext-tags-indicator";
    span.setAttribute("role", "button");
    span.setAttribute("aria-haspopup", "menu");
    span.setAttribute("aria-expanded", "false");
    span.tabIndex = 0;
    span.innerHTML = stacked ? STACKED_TAG_SVG : SINGLE_TAG_SVG;
    span.addEventListener("mousedown", this.onIndicatorMouseDown);
    span.addEventListener("keydown", this.onIndicatorKeyDown);
    return span;
  }

  private onIndicatorMouseDown = (e: MouseEvent): void => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    this.openDropdown();
    // Track click-and-drag: armed until mouseup. If mouseup lands on
    // a row, fire that row's selection.
    const onUp = (ev: MouseEvent) => {
      document.removeEventListener("mouseup", onUp, true);
      const target = ev.target as HTMLElement | null;
      if (!target || !this.dropdown) return;
      const row = target.closest(".ark-ext-tags-row") as HTMLElement | null;
      if (row && this.dropdown.contains(row)) {
        ev.preventDefault();
        ev.stopPropagation();
        this.activateRow(row);
      }
    };
    document.addEventListener("mouseup", onUp, true);
  };

  private onIndicatorKeyDown = (e: KeyboardEvent): void => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      this.openDropdown();
      const first = this.dropdown?.querySelector<HTMLElement>(".ark-ext-tags-row");
      first?.focus();
    }
  };

  /** Open the dropdown listing each <ark-tag> child. R2068, R2071 */
  private openDropdown(): void {
    if (this.dropdown) return;
    // Close any other open dropdown first.
    if (ArkExtTagsElement.activeOwner && ArkExtTagsElement.activeOwner !== this) {
      ArkExtTagsElement.activeOwner.closeDropdown();
    }
    const dd = document.createElement("div");
    dd.className = "ark-ext-tags-dropdown";
    dd.setAttribute("role", "menu");
    const rows = Array.from(this.querySelectorAll<HTMLElement>(":scope > ark-tag"));
    for (const tagEl of rows) {
      const row = document.createElement("button");
      row.className = "ark-ext-tags-row";
      row.type = "button";
      row.tabIndex = 0;
      row.setAttribute("role", "menuitem");
      row.dataset.tagName = tagEl.querySelector("name")?.textContent ?? "";
      row.dataset.tagValue = tagEl.querySelector("value")?.textContent ?? "";
      const nameSpan = document.createElement("span");
      nameSpan.className = "ark-ext-tags-row-name";
      nameSpan.textContent = `@${row.dataset.tagName}:`;
      const valSpan = document.createElement("span");
      valSpan.className = "ark-ext-tags-row-value";
      valSpan.textContent = row.dataset.tagValue ?? "";
      row.appendChild(nameSpan);
      row.appendChild(valSpan);
      row.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        this.activateRow(row);
      });
      row.addEventListener("keydown", (e) => this.onRowKeyDown(e, row));
      dd.appendChild(row);
    }
    this.appendChild(dd);
    this.dropdown = dd;
    this.indicator?.setAttribute("aria-expanded", "true");
    ArkExtTagsElement.activeOwner = this;
    // Grow the document so the dropdown is reachable (its absolute
    // positioning doesn't extend body height) and scroll it into view
    // if it would otherwise be clipped. Deferred past the opening
    // click event sequence: scrolling between mousedown and mouseup
    // would shift the page under the cursor, making the click event's
    // target land on <body> (LCA of mousedown and mouseup targets)
    // and falsely triggering the close-on-outside-click handler.
    setTimeout(() => this.ensureDropdownVisible(), 50);
    // Click-outside / Escape to dismiss
    setTimeout(() => {
      document.addEventListener("click", this.onDocClick, true);
      document.addEventListener("keydown", this.onDocKey, true);
    }, 0);
  }

  /** The dropdown is `position: absolute` so it doesn't grow the
   *  document height when it opens — when it lands near the bottom of
   *  the page it gets clipped beneath the viewport with no way to
   *  scroll to it. Add temporary `padding-bottom` on body to make the
   *  page tall enough, then scroll the dropdown into view. */
  private ensureDropdownVisible(): void {
    if (!this.dropdown) return;
    const rect = this.dropdown.getBoundingClientRect();
    const docBottom = document.documentElement.scrollHeight;
    const dropdownAbsBottom = rect.bottom + window.scrollY;
    const overflow = dropdownAbsBottom - docBottom;
    if (overflow > 0) {
      document.body.style.paddingBottom = `${overflow + 16}px`;
    }
    this.dropdown.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  private closeDropdown(): void {
    if (!this.dropdown) return;
    this.dropdown.remove();
    this.dropdown = null;
    // Release the body padding we may have added on open.
    document.body.style.paddingBottom = "";
    this.indicator?.setAttribute("aria-expanded", "false");
    if (ArkExtTagsElement.activeOwner === this) {
      ArkExtTagsElement.activeOwner = null;
    }
    document.removeEventListener("click", this.onDocClick, true);
    document.removeEventListener("keydown", this.onDocKey, true);
  }

  private onDocClick = (e: MouseEvent): void => {
    if (!this.dropdown) return;
    const t = e.target as Node;
    if (this.dropdown.contains(t) || this.indicator?.contains(t)) return;
    this.closeDropdown();
  };

  private onDocKey = (e: KeyboardEvent): void => {
    if (e.key === "Escape") {
      e.preventDefault();
      this.closeDropdown();
      this.indicator?.focus();
    }
  };

  private onRowKeyDown(e: KeyboardEvent, row: HTMLElement): void {
    const rows = Array.from(this.dropdown?.querySelectorAll<HTMLElement>(".ark-ext-tags-row") ?? []);
    const idx = rows.indexOf(row);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      rows[(idx + 1) % rows.length]?.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      rows[(idx - 1 + rows.length) % rows.length]?.focus();
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      this.activateRow(row);
    }
  }

  /** Open <ark-search> panel seeded with this row's tag:value.
   *  Public so TagOverviewSidebar can bypass the pick list. R2049 */
  openPanelForTag(tag: string, value: string): void {
    this.closeDropdown();
    if (!(window as any).document.arkSearchAPI) return;
    const search = document.createElement("ark-search") as HTMLElement & {
      api: unknown; tag: string; value: string;
    };
    search.api = (document as any).arkSearchAPI;
    search.tag = tag;
    search.value = value;
    search.addEventListener("close", () => search.remove());
    // Insert the panel after the chunk container that hosts this element.
    const chunk = this.closest(".ark-chunk");
    if (chunk?.parentNode) {
      chunk.parentNode.insertBefore(search, chunk.nextSibling);
    } else {
      this.after(search);
    }
  }

  private activateRow(row: HTMLElement): void {
    const tag = row.dataset.tagName ?? "";
    const value = row.dataset.tagValue ?? "";
    this.openPanelForTag(tag, value);
  }
}

const SINGLE_TAG_SVG = `<svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
<path d="M2 2v6.5a.5.5 0 0 0 .146.354l6 6a.5.5 0 0 0 .708 0l6.5-6.5a.5.5 0 0 0 0-.708l-6-6A.5.5 0 0 0 9 1.5H2.5A.5.5 0 0 0 2 2zm3 3a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z"/>
</svg>`;

const STACKED_TAG_SVG = `<svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
<path d="M0 4v6.5a.5.5 0 0 0 .146.354l6 6a.5.5 0 0 0 .708 0l.563-.563-6.062-6.062A1.5 1.5 0 0 1 1 9.232V4H0z"/>
<path d="M2 2v6.5a.5.5 0 0 0 .146.354l6 6a.5.5 0 0 0 .708 0l6.5-6.5a.5.5 0 0 0 0-.708l-6-6A.5.5 0 0 0 9 1.5H2.5A.5.5 0 0 0 2 2zm3 3a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z"/>
</svg>`;

// Outline icon used for the collapsed badge — three indented bars
// reading as "headings + structure", since the sidebar lists both
// headings and tags.
const OUTLINE_SVG = `<svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">
<rect x="1" y="3" width="13" height="1.2" rx="0.4"/>
<rect x="3" y="7.4" width="11" height="1.2" rx="0.4"/>
<rect x="5" y="11.9" width="9" height="1.2" rx="0.4"/>
</svg>`;

// ---- Sidebar --------------------------------------------------------
// R2032-R2064, R2083, R2084

type Category = "headings" | "inline" | "ext";

interface RowRecord {
  entry: Entry;
  rowEl: HTMLElement;
}

const PERSIST_KEY = "ark-tag-overview-width";
const DEFAULT_WIDTH = "25vw";
const MIN_WIDTH_PX = 160;        // badge stays readable
const MAX_RIGHT_GUTTER_PX = 48;  // viewport - 3rem ≈ 48px

class TagOverviewSidebar {
  private host: HTMLElement;
  private mode: Mode;
  private entries: Entry[] = [];
  private sections: Section[] = [];
  private rows: RowRecord[] = [];
  private root: HTMLElement | null = null;
  private badgeEl: HTMLElement | null = null;
  private filterBtn: HTMLButtonElement | null = null;
  private panelEl: HTMLElement | null = null;
  private resizeHandle: HTMLElement | null = null;
  private categoryPopover: HTMLElement | null = null;

  // Filter state
  private filterText = "";
  private filterCategories = new Set<Category>();   // empty = all (R2055)

  // Auto-track state
  private currentSectionId: string | null = null;
  private lastChosenId: string | null = null;
  private intersectObs: IntersectionObserver | null = null;
  private resizeObs: ResizeObserver | null = null;

  // Width persistence — keyed per non-collapsed mode (R2063)
  private widthByMode: Record<"abbreviated" | "full", string> = {
    abbreviated: DEFAULT_WIDTH,
    full: DEFAULT_WIDTH,
  };

  // Peek state — abbreviated mode only (R2042, R2044)
  private openPeekRow: HTMLElement | null = null;

  constructor(host: HTMLElement, initialMode: Mode = "abbreviated") {
    this.host = host;
    this.mode = initialMode;
  }

  mount(): void {
    this.entries = scanEntries(this.host);
    if (this.entries.length === 0) return;     // R2037: no chrome for empty
    this.sections = groupSections(this.entries);
    this.loadWidths();
    this.render();
    this.adjustOverlayButtons();
    if (typeof ResizeObserver !== "undefined" && this.root) {
      this.resizeObs = new ResizeObserver(() => this.adjustOverlayButtons());
      this.resizeObs.observe(this.root);
    }
    this.startAutoTrack();
  }

  // CRC: crc-TagOverviewSidebar.md | R2130, R2131
  /** Push toggle-btn left of the sidebar; also publish sidebar width. */
  private adjustOverlayButtons(): void {
    if (!this.root) return;
    const w = this.root.offsetWidth;
    document.documentElement.style.setProperty("--ark-tag-overview-width", `${w}px`);
    for (const id of ["toggle-btn"]) {
      const btn = document.getElementById(id);
      if (btn) btn.style.right = `calc(${w}px + 0.75em)`;
    }
  }

  private render(): void {
    this.root = document.createElement("div");
    this.root.className = "ark-tag-overview";
    this.root.dataset.mode = this.mode;
    this.root.setAttribute("role", "complementary");
    this.root.setAttribute("aria-label", "Tag overview sidebar");

    this.resizeHandle = this.buildResizeHandle();
    this.root.appendChild(this.resizeHandle);

    this.badgeEl = this.buildBadge();
    this.root.appendChild(this.badgeEl);

    this.panelEl = this.buildPanel();
    this.root.appendChild(this.panelEl);

    document.body.appendChild(this.root);
    this.applyMode();
  }

  /** R2034, R2038, R2039, R2052: badge with cycle button, filter
   *  input, and category-dropdown trigger. */
  private buildBadge(): HTMLElement {
    const badge = document.createElement("div");
    badge.className = "ark-tag-overview-badge";

    const cycle = document.createElement("button");
    cycle.type = "button";
    cycle.className = "ark-tag-overview-badge-cycle";
    cycle.setAttribute("aria-label", "Cycle sidebar mode");
    cycle.addEventListener("click", () => this.cycleMode());
    badge.appendChild(cycle);

    const filterBtn = document.createElement("button");
    filterBtn.type = "button";
    filterBtn.className = "ark-tag-overview-badge-filter";
    filterBtn.textContent = "▼";
    filterBtn.title = "Filter categories";
    filterBtn.setAttribute("aria-haspopup", "menu");
    filterBtn.setAttribute("aria-expanded", "false");
    filterBtn.addEventListener("click", (e) => {
      e.stopPropagation();
      this.toggleCategoryDropdown();
    });
    this.filterBtn = filterBtn;
    badge.appendChild(filterBtn);

    const input = document.createElement("input");
    input.type = "search";
    input.className = "ark-tag-overview-badge-input";
    input.placeholder = "filter…";
    input.setAttribute("aria-label", "Filter entries");
    input.addEventListener("input", () => {
      this.filterText = input.value;
      this.applyFilter();
      this.updateBadgeText();
    });
    badge.appendChild(input);

    return badge;
  }

  /** R2060, R2061: left-edge resize handle, mouse + touch. */
  private buildResizeHandle(): HTMLElement {
    const handle = document.createElement("div");
    handle.className = "ark-tag-overview-resize";
    handle.setAttribute("aria-hidden", "true");
    const onPointerDown = (clientX: number, isTouch: boolean) => {
      if (this.mode === "collapsed") return;       // R2061
      const startX = clientX;
      const startW = this.root?.offsetWidth ?? 0;
      const move = (mx: number) => {
        const delta = startX - mx;                  // dragging left grows width
        let next = startW + delta;
        if (next < MIN_WIDTH_PX) next = MIN_WIDTH_PX;
        const max = window.innerWidth - MAX_RIGHT_GUTTER_PX;
        if (next > max) next = max;
        this.applyWidth(`${next}px`);
      };
      const onMove = (e: MouseEvent) => move(e.clientX);
      const onTouchMove = (e: TouchEvent) => {
        if (e.touches.length) move(e.touches[0].clientX);
      };
      const stop = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", stop);
        document.removeEventListener("touchmove", onTouchMove);
        document.removeEventListener("touchend", stop);
        this.persistWidth();
      };
      if (isTouch) {
        document.addEventListener("touchmove", onTouchMove, { passive: true });
        document.addEventListener("touchend", stop);
      } else {
        document.addEventListener("mousemove", onMove);
        document.addEventListener("mouseup", stop);
      }
    };
    handle.addEventListener("mousedown", (e) => {
      e.preventDefault();
      onPointerDown(e.clientX, false);
    });
    handle.addEventListener("touchstart", (e) => {
      if (!e.touches.length) return;
      onPointerDown(e.touches[0].clientX, true);
    }, { passive: true });
    return handle;
  }

  /** Cycle modes per R2034. */
  private cycleMode(): void {
    this.mode = MODE_CYCLE[this.mode];
    this.applyMode();
  }

  private applyMode(): void {
    if (!this.root || !this.badgeEl) return;
    this.root.dataset.mode = this.mode;
    this.updateBadgeText();
    if (this.mode === "collapsed") {
      this.panelEl?.setAttribute("hidden", "");
      this.applyWidth("auto");                    // collapse to badge size
    } else {
      this.panelEl?.removeAttribute("hidden");
      this.applyWidth(this.widthByMode[this.mode]);
      this.renderPanel();
      this.applyFilter();
    }
    this.adjustOverlayButtons();
  }

  /** R2038, R2056: badge shows count + mode glyph in expanded modes;
   *  collapsed mode renders just the multi-tag SVG so it sits unobtrusive
   *  at the right edge. When a filter is active, show filtered count and
   *  add an "active" class to ▼. */
  private updateBadgeText(): void {
    if (!this.badgeEl) return;
    const cycle = this.badgeEl.querySelector(".ark-tag-overview-badge-cycle");
    if (!cycle) return;
    if (this.mode === "collapsed") {
      cycle.innerHTML = OUTLINE_SVG;
      const totalTags = this.entries.filter(e => e.kind !== "heading").length;
      const totalHeadings = this.entries.length - totalTags;
      const parts: string[] = [];
      if (totalHeadings) parts.push(`${totalHeadings} heading${totalHeadings === 1 ? "" : "s"}`);
      if (totalTags) parts.push(`${totalTags} tag${totalTags === 1 ? "" : "s"}`);
      cycle.setAttribute("title", `${parts.join(", ")} — click to expand`);
      return;
    }
    const totalTags = this.entries.filter(e => e.kind !== "heading").length;
    const filterActive = this.filterText.trim() !== "" || this.filterCategories.size > 0;
    const visible = filterActive ? this.visibleTagCount() : totalTags;
    const glyph = MODE_GLYPH[this.mode];
    if (filterActive) {
      cycle.textContent = `${glyph} ${visible}/${totalTags} tags`;
    } else {
      cycle.textContent = `${glyph} ${totalTags} tags`;
    }
    cycle.removeAttribute("title");
    this.filterBtn?.classList.toggle(
      "ark-tag-overview-badge-filter-active",
      this.filterCategories.size > 0,
    );
  }

  private visibleTagCount(): number {
    return this.rows.filter(r => r.entry.kind !== "heading" && !r.rowEl.hasAttribute("hidden")).length;
  }

  /** R2040-R2043: render entries grouped by section. */
  private buildPanel(): HTMLElement {
    const panel = document.createElement("div");
    panel.className = "ark-tag-overview-panel";
    return panel;
  }

  private renderPanel(): void {
    if (!this.panelEl) return;
    this.panelEl.innerHTML = "";
    this.rows = [];
    this.openPeekRow = null;
    for (const section of this.sections) {
      const secEl = document.createElement("section");
      secEl.className = "ark-tag-overview-section";
      if (section.anchor.kind === "heading") {
        secEl.appendChild(this.renderHeadingRow(section.anchor));
      }
      for (const entry of section.entries) {
        if (entry === section.anchor && entry.kind === "heading") continue;
        secEl.appendChild(this.renderEntryRow(entry));
      }
      this.panelEl.appendChild(secEl);
    }
  }

  private renderHeadingRow(entry: Entry): HTMLElement {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "ark-tag-overview-row ark-tag-overview-row-heading";
    if (entry.headingLevel) {
      row.dataset.level = String(entry.headingLevel);
    }
    row.dataset.entryId = entry.elementId;
    row.textContent = entry.headingText ?? "";
    row.addEventListener("click", () => this.scrollTo(entry.elementId));
    this.rows.push({ entry, rowEl: row });
    return row;
  }

  private renderEntryRow(entry: Entry): HTMLElement {
    const row = document.createElement("div");
    row.className =
      "ark-tag-overview-row " +
      (entry.kind === "ext" ? "ark-tag-overview-row-ext" : "ark-tag-overview-row-inline");
    row.dataset.entryId = entry.elementId;

    const main = document.createElement("button");
    main.type = "button";
    main.className = "ark-tag-overview-row-main";
    if (entry.kind === "ext") {
      const glyph = document.createElement("span");
      glyph.className = "ark-tag-overview-virtual-glyph";
      glyph.textContent = "⊕";
      main.appendChild(glyph);
    }
    const nameSpan = document.createElement("span");
    nameSpan.className = "ark-tag-overview-name";
    nameSpan.textContent = `@${entry.tagName}:`;
    main.appendChild(nameSpan);
    if (this.mode === "full" && entry.tagValue) {
      const valSpan = document.createElement("span");
      valSpan.className = "ark-tag-overview-value";
      valSpan.textContent = entry.tagValue;
      main.appendChild(valSpan);
    }
    main.addEventListener("click", () => this.scrollTo(entry.elementId));
    row.appendChild(main);

    // 🔍 search dispatch. R2048, R2049.
    const search = document.createElement("button");
    search.type = "button";
    search.className = "ark-tag-overview-icon-search";
    search.title = "Search this tag";
    search.setAttribute("aria-label", `Search @${entry.tagName ?? ""}`);
    search.textContent = "🔍";
    search.addEventListener("click", (e) => {
      e.stopPropagation();
      this.dispatchSearch(entry);
    });
    row.appendChild(search);

    // ↗ external link with hover tooltip. R2050, R2051.
    if (entry.kind === "ext" && entry.externalFile) {
      const ext = document.createElement("a");
      ext.className = "ark-tag-overview-icon-ext";
      ext.textContent = "↗";
      ext.href = "/content" + entry.externalFile;
      ext.setAttribute("aria-label", "Go to source document");
      ext.addEventListener("click", (e) => e.stopPropagation());
      this.attachExtTooltip(ext, entry);
      row.appendChild(ext);
    }

    // R2042, R2044: abbreviated-mode hover peek (desktop). The peek
    // is an inline value display revealed on hover; tap behavior on
    // touch devices is deferred (touch peek conflicts with row scroll
    // — needs a separate tap target).
    if (entry.tagValue) {
      row.addEventListener("mouseenter", () => this.openPeek(row, entry.tagValue!));
      row.addEventListener("mouseleave", () => {
        if (row === this.openPeekRow) this.closeAnyPeek();
      });
    }

    this.rows.push({ entry, rowEl: row });
    return row;
  }

  /** R2051: tooltip on ↗ — DEFINITION-PATH / divider / THIS-PATH /
   *  optional `anchor: ANCHOR-SPEC`. */
  private attachExtTooltip(target: HTMLElement, entry: Entry): void {
    let tip: HTMLElement | null = null;
    const show = () => {
      if (tip || !target.isConnected) return;
      tip = document.createElement("div");
      tip.className = "ark-tag-overview-tooltip";
      tip.setAttribute("role", "tooltip");
      const def = document.createElement("div");
      def.textContent = entry.externalFile ?? "";
      const div = document.createElement("hr");
      const here = document.createElement("div");
      here.textContent = location.pathname.replace(/^\/content/, "");
      tip.appendChild(def);
      tip.appendChild(div);
      tip.appendChild(here);
      if (entry.externalTarget) {
        const anchor = document.createElement("div");
        anchor.textContent = `anchor: ${entry.externalTarget}`;
        tip.appendChild(anchor);
      }
      document.body.appendChild(tip);
      const r = target.getBoundingClientRect();
      tip.style.top = `${r.bottom + 4}px`;
      tip.style.left = `${Math.max(8, r.right - tip.offsetWidth)}px`;
    };
    const hide = () => {
      tip?.remove();
      tip = null;
    };
    target.addEventListener("mouseenter", show);
    target.addEventListener("mouseleave", hide);
    target.addEventListener("focus", show);
    target.addEventListener("blur", hide);
  }

  private openPeek(row: HTMLElement, value: string): void {
    if (this.mode !== "abbreviated" || row === this.openPeekRow) return;
    this.closeAnyPeek();
    const main = row.querySelector<HTMLElement>(".ark-tag-overview-row-main");
    if (!main) return;
    const peek = document.createElement("span");
    peek.className = "ark-tag-overview-value ark-tag-overview-value-peek";
    peek.textContent = value;
    main.appendChild(peek);
    this.openPeekRow = row;
  }

  private closeAnyPeek(): void {
    this.openPeekRow?.querySelector(".ark-tag-overview-value-peek")?.remove();
    this.openPeekRow = null;
  }

  // ---- Filter ---------------------------------------------------------

  /** R2052-R2054: tokenized substring filter, mode-aware visibility,
   *  case-insensitive. Empty filterCategories means all categories.
   *  Highlights matched substrings using the existing ark search
   *  highlight class. */
  private applyFilter(): void {
    const tokens = this.filterText.toLowerCase().split(/\s+/).filter(Boolean);
    const cats = this.filterCategories;
    const allowedKinds = (e: Entry) => {
      if (cats.size === 0) return true;
      if (e.kind === "heading") return cats.has("headings");
      if (e.kind === "inline") return cats.has("inline");
      return cats.has("ext");
    };
    for (const rec of this.rows) {
      const e = rec.entry;
      if (!allowedKinds(e)) {
        rec.rowEl.setAttribute("hidden", "");
        continue;
      }
      const txt = this.entryFilterText(e).toLowerCase();
      const ok = tokens.every(t => txt.includes(t));
      if (ok) {
        rec.rowEl.removeAttribute("hidden");
        this.highlightTokens(rec.rowEl, tokens);
      } else {
        rec.rowEl.setAttribute("hidden", "");
      }
    }
  }

  /** Mode-aware visible text per spec — abbreviated shows name +
   *  heading text only; full also includes value. Hover-revealed
   *  values don't count. */
  private entryFilterText(e: Entry): string {
    if (e.kind === "heading") return e.headingText ?? "";
    const name = `@${e.tagName ?? ""}`;
    if (this.mode === "full") return `${name} ${e.tagValue ?? ""}`;
    return name;
  }

  /** R2054: highlight matched substrings reusing the search highlight
   *  class. Strips and re-applies on each filter change. */
  private highlightTokens(row: HTMLElement, tokens: string[]): void {
    const targets = row.querySelectorAll<HTMLElement>(
      ".ark-tag-overview-row-heading, .ark-tag-overview-name, .ark-tag-overview-value",
    );
    // Skip rows that are themselves the heading button (single text span).
    const allTargets = row.matches(".ark-tag-overview-row-heading") ? [row] : Array.from(targets);
    for (const el of allTargets) {
      const original = el.dataset.rawText ?? el.textContent ?? "";
      el.dataset.rawText = original;
      if (tokens.length === 0) {
        el.textContent = original;
        continue;
      }
      // Build a regex that matches any token, longest-first to avoid
      // overlap surprises. Case-insensitive.
      const sorted = [...tokens].sort((a, b) => b.length - a.length);
      const re = new RegExp(sorted.map(escapeRegex).join("|"), "gi");
      el.innerHTML = "";
      let last = 0;
      const text = original;
      for (const m of text.matchAll(re)) {
        const idx = m.index ?? 0;
        if (idx > last) el.appendChild(document.createTextNode(text.slice(last, idx)));
        const mark = document.createElement("mark");
        mark.className = "ark-search-highlight";
        mark.textContent = m[0];
        el.appendChild(mark);
        last = idx + m[0].length;
      }
      if (last < text.length) el.appendChild(document.createTextNode(text.slice(last)));
    }
  }

  // ---- Category dropdown ---------------------------------------------

  /** R2039, R2055: popover with three checkboxes. Empty = all. */
  private toggleCategoryDropdown(): void {
    if (this.categoryPopover) {
      this.closeCategoryDropdown();
      return;
    }
    if (!this.badgeEl || !this.filterBtn) return;
    const pop = document.createElement("div");
    pop.className = "ark-tag-overview-category-popover";
    pop.setAttribute("role", "menu");
    const make = (cat: Category, label: string) => {
      const lbl = document.createElement("label");
      lbl.className = "ark-tag-overview-category-row";
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = this.filterCategories.has(cat);
      cb.addEventListener("change", () => {
        if (cb.checked) this.filterCategories.add(cat);
        else this.filterCategories.delete(cat);
        this.applyFilter();
        this.updateBadgeText();
      });
      lbl.appendChild(cb);
      const span = document.createElement("span");
      span.textContent = label;
      lbl.appendChild(span);
      pop.appendChild(lbl);
    };
    make("headings", "Headings");
    make("inline", "Inline tags");
    make("ext", "Ext tags");
    this.badgeEl.appendChild(pop);
    this.categoryPopover = pop;
    this.filterBtn.setAttribute("aria-expanded", "true");
    setTimeout(() => {
      document.addEventListener("click", this.onCategoryDocClick, true);
      document.addEventListener("keydown", this.onCategoryDocKey, true);
    }, 0);
  }

  private closeCategoryDropdown(): void {
    this.categoryPopover?.remove();
    this.categoryPopover = null;
    this.filterBtn?.setAttribute("aria-expanded", "false");
    document.removeEventListener("click", this.onCategoryDocClick, true);
    document.removeEventListener("keydown", this.onCategoryDocKey, true);
  }

  private onCategoryDocClick = (e: MouseEvent): void => {
    const t = e.target as Node;
    if (this.categoryPopover?.contains(t) || this.filterBtn?.contains(t)) return;
    this.closeCategoryDropdown();
  };

  private onCategoryDocKey = (e: KeyboardEvent): void => {
    if (e.key === "Escape") {
      e.preventDefault();
      this.closeCategoryDropdown();
    }
  };

  // ---- Width persistence ---------------------------------------------

  /** R2062, R2063: load per-mode widths from localStorage (interim
   *  Stage B substrate; HostAPI / I-record swap is Stage C). */
  private loadWidths(): void {
    try {
      const raw = localStorage.getItem(PERSIST_KEY);
      if (!raw) return;
      const obj = JSON.parse(raw);
      if (typeof obj?.abbreviated === "string") this.widthByMode.abbreviated = obj.abbreviated;
      if (typeof obj?.full === "string") this.widthByMode.full = obj.full;
    } catch {
      /* noop */
    }
  }

  private persistWidth(): void {
    if (this.mode === "collapsed") return;
    if (!this.root) return;
    this.widthByMode[this.mode] = `${this.root.offsetWidth}px`;
    try {
      localStorage.setItem(PERSIST_KEY, JSON.stringify(this.widthByMode));
    } catch {
      /* noop */
    }
  }

  private applyWidth(w: string): void {
    if (!this.root) return;
    if (w === "auto") {
      this.root.style.removeProperty("width");
    } else {
      this.root.style.width = w;
    }
    this.adjustOverlayButtons();
  }

  // ---- Auto-track ----------------------------------------------------

  /** R2045, R2058: IntersectionObserver on headings + ext-tags so we
   *  can highlight the current section. Falls back silently if
   *  IntersectionObserver isn't available. */
  private startAutoTrack(): void {
    if (typeof IntersectionObserver === "undefined") return;
    const ids = new Set<string>();
    for (const e of this.entries) {
      if ((e.kind === "heading" || e.kind === "ext") && e.elementId) {
        ids.add(e.elementId);
      }
    }
    const targets: HTMLElement[] = [];
    for (const id of ids) {
      const el = this.host.querySelector(`#${CSS.escape(id)}`) as HTMLElement | null;
      if (el) targets.push(el);
    }
    if (targets.length === 0) return;
    this.intersectObs = new IntersectionObserver((records) => {
      for (const r of records) {
        const id = (r.target as HTMLElement).id;
        if (!id) continue;
        if (r.isIntersecting && r.boundingClientRect.top <= 80) {
          this.currentSectionId = id;
        }
      }
      this.refreshAutoTrackHighlight();
    }, { rootMargin: "0px 0px -85% 0px", threshold: [0, 1] });
    for (const t of targets) this.intersectObs.observe(t);
  }

  private refreshAutoTrackHighlight(): void {
    if (!this.panelEl || !this.currentSectionId) return;
    let chosenId = this.currentSectionId;
    // Fall back to nearest visible entry above if current is filtered out.
    const idx = this.rows.findIndex(r => r.entry.elementId === chosenId);
    if (idx >= 0 && this.rows[idx].rowEl.hasAttribute("hidden")) {
      for (let i = idx - 1; i >= 0; i--) {
        if (!this.rows[i].rowEl.hasAttribute("hidden")) {
          chosenId = this.rows[i].entry.elementId;
          break;
        }
      }
    }
    if (chosenId === this.lastChosenId) return;
    this.lastChosenId = chosenId;
    for (const r of this.rows) {
      r.rowEl.classList.toggle(
        "ark-tag-overview-row-current",
        r.entry.elementId === chosenId,
      );
    }
  }

  /** R2046, R2047: scroll the document so the target id is visible.
   *  For ext entries the <ark-tag> child of <ark-ext-tags> is
   *  display:none (it's a metadata carrier, not the visible widget) —
   *  scrollIntoView would be a no-op, so resolve to the parent
   *  <ark-ext-tags> host before scrolling. */
  private scrollTo(id: string): void {
    if (!id) return;
    let target = this.host.querySelector(`#${CSS.escape(id)}`) as HTMLElement | null;
    if (!target) return;
    const extHost = target.closest(EXT_TAGS_TAG) as HTMLElement | null;
    if (extHost) target = extHost;
    target.scrollIntoView({ behavior: "smooth", block: "start" });
  }

  /** R2048: inline tag — dispatch click on body <ark-tag>. R2049: ext
   *  tag — scroll the parent <ark-ext-tags> into view, then call
   *  openPanelForTag on it. */
  private dispatchSearch(entry: Entry): void {
    const target = this.host.querySelector(`#${CSS.escape(entry.elementId)}`) as HTMLElement | null;
    if (!target) return;
    if (entry.kind === "inline") {
      // Click the corresponding <ark-tag>'s action affordance. The
      // existing inline element handles scroll + panel open.
      const action = target.querySelector<HTMLElement>(".ark-tag-action");
      if (action) {
        action.click();
      } else {
        target.click();
      }
      return;
    }
    // ext entry: parent <ark-ext-tags> exposes openPanelForTag and is
    // the visible host (the inner <ark-tag> is display:none).
    const host = target.closest(EXT_TAGS_TAG) as ArkExtTagsElement | null;
    if (host) {
      host.scrollIntoView({ behavior: "smooth", block: "start" });
      if (entry.tagName) {
        host.openPanelForTag(entry.tagName, entry.tagValue ?? "");
      }
    }
  }
}

// ---- DOM scan -------------------------------------------------------

/** R2040-R2043: walk document order, classify by element. Inline tags
 *  are <ark-tag> NOT inside <ark-ext-tags>; ext tags are <ark-tag>
 *  children of <ark-ext-tags>. Headings are <h1>-<h6> and
 *  <ark-heading>. Each entry needs an id (assigned server-side).
 *
 *  Chunk-local reorder: the server emits `<ark-ext-tags>` blocks
 *  before the heading inside each `<div class="ark-chunk">`, but
 *  the user thinks of them as annotations *on* the heading's
 *  chunk. So entries are grouped by their containing chunk and,
 *  within each chunk, the heading is moved to the front. */
function scanEntries(host: HTMLElement): Entry[] {
  type WalkEntry = { entry: Entry; chunkEl: Element | null };
  const walked: WalkEntry[] = [];
  let headingCount = 0;
  const walker = document.createTreeWalker(host, NodeFilter.SHOW_ELEMENT, {
    acceptNode(n) {
      const el = n as Element;
      const tag = el.tagName.toLowerCase();
      if (tag === "h1" || tag === "h2" || tag === "h3" || tag === "h4" || tag === "h5" || tag === "h6") {
        return NodeFilter.FILTER_ACCEPT;
      }
      if (tag === "ark-heading") return NodeFilter.FILTER_ACCEPT;
      if (tag === "ark-tag") return NodeFilter.FILTER_ACCEPT;
      return NodeFilter.FILTER_SKIP;
    },
  });
  let node: Node | null = walker.currentNode;
  while ((node = walker.nextNode())) {
    const el = node as HTMLElement;
    const tag = el.tagName.toLowerCase();
    const chunkEl = el.closest("div.ark-chunk");
    const hMatch = /^h([1-6])$/.exec(tag);
    if (hMatch) {
      headingCount++;
      walked.push({
        entry: {
          kind: "heading",
          elementId: el.id,
          headingText: (el.textContent ?? "").trim(),
          headingLevel: Number(hMatch[1]),
        },
        chunkEl,
      });
      continue;
    }
    if (tag === "ark-heading") {
      headingCount++;
      walked.push({
        entry: {
          kind: "heading",
          elementId: el.id,
          headingText: `Heading ${headingCount}`,
          headingLevel: 0,
        },
        chunkEl,
      });
      continue;
    }
    if (tag === "ark-tag") {
      const inExt = el.parentElement?.tagName.toLowerCase() === EXT_TAGS_TAG;
      const name = el.querySelector("name")?.textContent ?? "";
      const value = el.querySelector("value")?.textContent ?? "";
      walked.push({
        entry: inExt
          ? {
              kind: "ext",
              elementId: el.id,
              tagName: name,
              tagValue: value,
              externalFile: el.getAttribute("externalFile") ?? "",
              externalTarget: el.getAttribute("externalTarget") ?? "",
            }
          : {
              kind: "inline",
              elementId: el.id,
              tagName: name,
              tagValue: value,
            },
        chunkEl,
      });
    }
  }

  // Group by containing chunk, preserving the order each chunk was
  // first seen. Entries without a chunk (rare; should not happen for
  // well-formed content views) keep their walk order.
  const chunkOrder: Array<Element | null> = [];
  const chunkBuckets = new Map<Element | null, WalkEntry[]>();
  for (const w of walked) {
    let bucket = chunkBuckets.get(w.chunkEl);
    if (!bucket) {
      bucket = [];
      chunkBuckets.set(w.chunkEl, bucket);
      chunkOrder.push(w.chunkEl);
    }
    bucket.push(w);
  }

  // Within each chunk, hoist the (first) heading to the front so
  // ext-tags and inline tags read as annotations under the heading.
  const out: Entry[] = [];
  for (const chunk of chunkOrder) {
    const bucket = chunkBuckets.get(chunk)!;
    const headingIdx = bucket.findIndex(w => w.entry.kind === "heading");
    if (headingIdx > 0) {
      const [headingEntry] = bucket.splice(headingIdx, 1);
      bucket.unshift(headingEntry);
    }
    for (const w of bucket) out.push(w.entry);
  }
  return out;
}

/** A section starts at a heading or at a tag/ext that has no
 *  preceding heading. Subsequent tags belong to the most recent
 *  section. */
function groupSections(entries: Entry[]): Section[] {
  const out: Section[] = [];
  let current: Section | null = null;
  for (const entry of entries) {
    if (entry.kind === "heading" || !current) {
      current = { anchor: entry, entries: [entry] };
      out.push(current);
    } else {
      current.entries.push(entry);
    }
  }
  return out;
}

// ---- Bootstrap + CSS ------------------------------------------------

const SIDEBAR_CSS = `
.ark-tag-overview {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: 25vw;
  z-index: 100;
  display: flex;
  flex-direction: column;
  font-family: var(--term-mono, monospace);
  font-size: 0.85em;
  color: var(--term-text, #e0e0e8);
  background: var(--term-bg-panel, #0d0d14);
  border-left: 1px solid var(--term-border, #2a2a3a);
  pointer-events: auto;
}
.ark-tag-overview[data-mode="collapsed"] {
  width: auto;
  height: auto;
  bottom: auto;
  background: transparent;
  border-left: none;
}
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-panel,
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-resize,
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-badge-input,
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-badge-filter {
  display: none;
}
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-badge {
  padding: 0.15em;
  background: transparent;
  border-bottom: none;
  gap: 0;
}
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-badge-cycle {
  display: inline-flex;
  align-items: center;
  padding: 0.2em 0.3em;
  opacity: 0.85;
}
.ark-tag-overview[data-mode="collapsed"] .ark-tag-overview-badge-cycle:hover {
  opacity: 1;
}
.ark-tag-overview-resize {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 6px;
  margin-left: -3px;
  cursor: ew-resize;
  background: transparent;
  z-index: 1;
}
.ark-tag-overview-resize:hover,
.ark-tag-overview-resize:active {
  background: var(--term-accent-dim, rgba(224,122,71,0.35));
}
.ark-tag-overview-badge {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  gap: 0.35em;
  padding: 0.5em 0.75em;
  border-bottom: 1px solid var(--term-border, #2a2a3a);
  background: var(--term-bg-elevated, #12121a);
}
.ark-tag-overview-badge-cycle,
.ark-tag-overview-badge-filter {
  background: var(--term-bg, #0a0a0f);
  color: var(--term-accent-bright, #ff9966);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 6px;
  padding: 0.3em 0.65em;
  font: inherit;
  cursor: pointer;
}
.ark-tag-overview-badge-cycle:hover,
.ark-tag-overview-badge-filter:not(:disabled):hover {
  border-color: var(--term-accent, #E07A47);
}
.ark-tag-overview-badge-filter-active {
  background: var(--term-accent-dim, rgba(224,122,71,0.2));
  border-color: var(--term-accent, #E07A47);
}
.ark-tag-overview-badge-input {
  flex: 1;
  min-width: 6em;
  background: var(--term-bg, #0a0a0f);
  color: var(--term-text, #e0e0e8);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 6px;
  padding: 0.3em 0.55em;
  font: inherit;
  outline: none;
  caret-color: var(--term-accent-bright, #ff9966);
}
.ark-tag-overview-badge-input:focus {
  border-color: var(--term-accent, #E07A47);
  box-shadow: 0 0 0 2px var(--term-accent-glow, rgba(224,122,71,0.4));
}
.ark-tag-overview-category-popover {
  position: absolute;
  top: 100%;
  right: 0.5em;
  z-index: 20;
  display: flex;
  flex-direction: column;
  gap: 0.25em;
  padding: 0.45em 0.65em;
  background: var(--term-bg-elevated, #12121a);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.5);
}
.ark-tag-overview-category-row {
  display: flex;
  align-items: center;
  gap: 0.4em;
  cursor: pointer;
}
.ark-tag-overview-panel {
  flex: 1;
  overflow-y: auto;
  padding: 0.5em 0.5em 1em;
}
.ark-tag-overview-section {
  margin-bottom: 0.6em;
}
.ark-tag-overview-row {
  display: flex;
  align-items: center;
  gap: 0.35em;
  padding: 0.25em 0.4em;
  border-radius: 4px;
}
.ark-tag-overview-row[hidden] {
  display: none;
}
.ark-tag-overview-row-heading {
  background: transparent;
  border: none;
  color: var(--term-accent-bright, #ff9966);
  font-weight: 600;
  cursor: pointer;
  text-align: left;
  width: 100%;
  font: inherit;
  padding: 0.35em 0.4em;
}
.ark-tag-overview-row-heading[data-level="2"] { padding-left: 1em; }
.ark-tag-overview-row-heading[data-level="3"] { padding-left: 1.6em; }
.ark-tag-overview-row-heading[data-level="4"] { padding-left: 2.2em; }
.ark-tag-overview-row-heading[data-level="5"] { padding-left: 2.8em; }
.ark-tag-overview-row-heading[data-level="6"] { padding-left: 3.4em; }
.ark-tag-overview-row-heading:hover {
  background: var(--term-bg-elevated, #12121a);
}
.ark-tag-overview-row-main {
  flex: 1;
  background: transparent;
  border: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
  text-align: left;
  display: flex;
  align-items: baseline;
  gap: 0.3em;
  padding: 0;
}
.ark-tag-overview-row-main:hover {
  text-decoration: underline;
}
.ark-tag-overview-name {
  color: var(--term-accent-bright, #ff9966);
}
.ark-tag-overview-value {
  color: var(--term-success, #4ade80);
}
.ark-tag-overview-virtual-glyph {
  color: var(--term-accent, #E07A47);
  font-size: 0.95em;
}
.ark-tag-overview-icon-search,
.ark-tag-overview-icon-ext {
  background: transparent;
  border: none;
  color: var(--term-text-dim, #8888a0);
  cursor: pointer;
  font-size: 0.9em;
  padding: 0 0.2em;
  text-decoration: none;
}
.ark-tag-overview-icon-search:hover,
.ark-tag-overview-icon-ext:hover {
  color: var(--term-accent-bright, #ff9966);
}
.ark-tag-overview-row-current {
  background: var(--term-accent-dim, rgba(224,122,71,0.2));
  border-radius: 4px;
}
.ark-tag-overview-value-peek {
  margin-left: 0.4em;
  color: var(--term-success, #4ade80);
}
.ark-tag-overview-tooltip {
  position: fixed;
  z-index: 200;
  display: flex;
  flex-direction: column;
  gap: 0.2em;
  padding: 0.5em 0.7em;
  background: var(--term-bg-elevated, #12121a);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.5);
  font-size: 0.8em;
  font-family: var(--term-mono, monospace);
  color: var(--term-text, #e0e0e8);
  pointer-events: none;
  max-width: 32em;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ark-tag-overview-tooltip hr {
  border: none;
  border-top: 1px solid var(--term-border, #2a2a3a);
  margin: 0.15em 0;
  width: 100%;
}

/* <ark-ext-tags> body indicator */
ark-ext-tags {
  float: left;
  margin: 0.15em 0.4em 0 0;
  z-index: 5;
}
ark-ext-tags > ark-tag {
  display: none;
}
.ark-chunk { position: relative; }
pdf-chunk ark-ext-tags {
  position: absolute;
  float: none;
  margin: 0;
}
/* R2131 */
body[data-pdf-host] ark-search {
  display: block;
  margin-left: 3em;
  margin-right: calc(max(3em, var(--ark-tag-overview-width, 0px)) + 1em);
}
.ark-ext-tags-indicator {
  pointer-events: auto;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  color: var(--term-accent-bright, #ff9966);
  background: var(--term-bg, #0a0a0f);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 4px;
  padding: 0.1em 0.25em;
  opacity: 0.85;
}
.ark-ext-tags-indicator:hover,
.ark-ext-tags-indicator:focus {
  opacity: 1;
  outline: none;
  border-color: var(--term-accent, #E07A47);
}
.ark-ext-tags-dropdown {
  position: absolute;
  left: 0;
  top: 100%;
  z-index: 10;
  display: flex;
  flex-direction: column;
  background: var(--term-bg-elevated, #12121a);
  border: 1px solid var(--term-border, #2a2a3a);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,0.5);
  pointer-events: auto;
  min-width: 12em;
  padding: 0.25em;
}
.ark-ext-tags-row {
  background: transparent;
  border: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
  text-align: left;
  display: flex;
  gap: 0.4em;
  padding: 0.35em 0.5em;
  border-radius: 4px;
}
.ark-ext-tags-row:hover,
.ark-ext-tags-row:focus {
  background: var(--term-accent-dim, rgba(224,122,71,0.2));
  outline: none;
}
.ark-ext-tags-row-name {
  color: var(--term-accent-bright, #ff9966);
}
.ark-ext-tags-row-value {
  color: var(--term-success, #4ade80);
}
`;

function injectCss(): void {
  if (document.getElementById("ark-tag-overview-css")) return;
  const style = document.createElement("style");
  style.id = "ark-tag-overview-css";
  style.textContent = SIDEBAR_CSS;
  document.head.appendChild(style);
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function bootstrap(): void {
  injectCss();
  if (!customElements.get(EXT_TAGS_TAG)) {
    customElements.define(EXT_TAGS_TAG, ArkExtTagsElement);
  }
  const host = document.getElementById("content") ?? document.body;
  // Search-result iframes append ?tag-overview=collapsed so previews
  // start showing only the right-edge icon instead of the full sidebar.
  const param = new URL(location.href).searchParams.get("tag-overview");
  const initialMode: Mode = param === "collapsed" ? "collapsed" : "abbreviated";
  const sidebar = new TagOverviewSidebar(host, initialMode);
  sidebar.mount();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", bootstrap);
} else {
  bootstrap();
}
