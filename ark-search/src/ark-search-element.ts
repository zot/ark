// CRC: crc-ArkSearchElement.md | Seq: seq-tag-click.md
// R1356-R1367, R1372, R1373, R1377, R1386-R1394, R1406-R1422

import type {
  SearchAPI,
  SearchResultGroup,
  TagMatch,
  ChunkFilterParam,
} from "./search-api";

/** Source phase for visual treatment. */
type Phase = "trigram" | "candidate" | "curated" | "rejected";

/** A result group tagged with its source phase. */
interface PhasedGroup {
  group: SearchResultGroup;
  phase: Phase;
}

type FilterMode = "contains" | "fuzzy" | "regex" | "tag" | "files";
type Polarity = "with" | "without";
type TagMatchMode = "exact" | "regex" | "fuzzy";
type TagNameMatch = "contains" | "exact";

/** Internal filter row state. */
interface FilterRow {
  id: number;
  polarity: Polarity;
  mode: FilterMode;
  query: string;
  tagName: string;
  tagValue: string;
  tagMatchMode: TagMatchMode;
  tagNameMatch: TagNameMatch;
}

/** OR group: one or more rows with OR semantics. R1438 */
interface FilterGroup {
  id: number;
  polarity: Polarity;
  rows: FilterRow[];
}

/** Source type toggle state. */
interface SourceToggle {
  name: string;
  pattern: string;
  active: boolean;
}

const SEARCH_MODES = ["tag", "contains", "fuzzy", "regex"] as const;
type SearchMode = (typeof SEARCH_MODES)[number];
const FILTER_MODES: FilterMode[] = ["contains", "fuzzy", "regex", "tag", "files"];
const TAG_MATCH_LABELS: Record<TagMatchMode, string> = { exact: "Aa", regex: ".*", fuzzy: "~" };
const TAG_MATCH_CYCLE: TagMatchMode[] = ["exact", "regex", "fuzzy"];
const TAG_NAME_MATCH_LABELS: Record<TagNameMatch, string> = { contains: "~", exact: "=" };
const TAG_NAME_MATCH_CYCLE: TagNameMatch[] = ["contains", "exact"];

/** Tag name characters per ark's tag parser — start is letter, then
 *  word chars plus `.` and `-`. Used to anchor contains-name regexes. */
const TAG_NAME_CHARS = "[\\w.-]";

/** Tokenize a tag value on whitespace — word order doesn't matter,
 *  each token matches independently and highlights separately. */
function tokenizeValue(raw: string): string[] {
  return raw.split(/\s+/).filter(Boolean);
}

/** Build the `@name:` prefix regex, respecting contains vs exact
 *  name match and the tag boundary heuristic `(^|\s)@NAME:`. */
function tagPrefixRegex(name: string, match: TagNameMatch): string {
  const escaped = escapeRegex(name);
  const pat = match === "contains"
    ? `${TAG_NAME_CHARS}*${escaped}${TAG_NAME_CHARS}*`
    : escaped;
  return `(?:(?<=\\s)|^)@${pat}:`;
}

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// --- Filter persistence R1448-R1453 ---

const STORAGE_KEY = "ark-search-filters";

/** Serializable filter preset. */
interface FilterPreset {
  groups: FilterGroup[];
}

function loadPresets(): Record<string, FilterPreset> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch { return {}; }
}

function savePresets(presets: Record<string, FilterPreset>): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(presets));
}

let nextFilterId = 1;
function newFilterRow(mode: FilterMode = "contains", polarity: Polarity = "with"): FilterRow {
  return {
    id: nextFilterId++,
    polarity,
    mode,
    query: "",
    tagName: "",
    tagValue: "",
    tagMatchMode: "exact",
    tagNameMatch: "contains",
  };
}

/** Create an OR group from a list of rows. R1438 */
function newFilterGroup(polarity: Polarity, rows: FilterRow[]): FilterGroup {
  return { id: nextFilterId++, polarity, rows };
}

/**
 * `<ark-search>` custom element — search panel with stacked
 * filter rows and three-phase progressive search.
 *
 * Properties (set by host after creation):
 * - api: SearchAPI — required
 * - tag: string — initial tag name (sets base query to regex tag search)
 * - value: string — initial value filter
 *
 * Dispatches 'close' CustomEvent when close button clicked.
 */
export class ArkSearchElement extends HTMLElement {
  private _api: SearchAPI | null = null;
  private _tag = "";
  private _value = "";
  private _initialized = false;

  // Base query bar
  private queryInput!: HTMLInputElement;
  private modeSelect!: HTMLSelectElement;
  private barInputs!: HTMLElement;
  private _searchMode: SearchMode = "tag";

  // Base tag mode state (when _searchMode === "tag")
  private _baseTagName = "";
  private _baseTagValue = "";
  private _baseTagMatch: TagMatchMode = "exact";
  private _baseTagNameMatch: TagNameMatch = "contains";

  // Filter groups (each group has 1+ rows with OR semantics) R1438
  private filterGroups: FilterGroup[] = [];
  private filtersContainer!: HTMLElement;

  // Source-type bar
  private sourceToggles: SourceToggle[] = [
    { name: "data", pattern: "", active: true },
    { name: "project", pattern: "*.md", active: true },
    { name: "memory", pattern: "**/knowledge/**", active: true },
    { name: "chats", pattern: "**/*.jsonl", active: true },
  ];
  private sourceBar!: HTMLElement;
  private chipBar!: HTMLElement;

  // Results
  private resultsEl!: HTMLElement;
  private debounceTimer: ReturnType<typeof setTimeout> | null = null;
  private lazyObserver: IntersectionObserver | null = null;
  private heightListener: ((e: MessageEvent) => void) | null = null;

  // Cache of group elements keyed by path — lets us reuse existing
  // iframes across phase updates and between searches that share paths,
  // instead of clearing and rebuilding the whole results DOM. R1464.
  //
  // The signature is split so we can detect "same chunks, only highlights
  // changed" (e.g. the user edited the value in a tag search that still
  // returns the same file) and update the iframes in place via postMessage
  // instead of rebuilding them. R1466
  private resultEls = new Map<
    string,
    { el: HTMLElement; chunkSig: string; highlightSig: string; phase: Phase }
  >();

  // Progressive search state
  private searchGeneration = 0;
  private phasedResults: PhasedGroup[] = [];

  /** If set, use this query directly and hide the query bar. */
  private _query = "";
  /** Hide the query bar (for embedded use, e.g. ark-search code blocks). */
  private _hideBar = false;

  get api(): SearchAPI | null { return this._api; }
  set api(v: SearchAPI | null) {
    this._api = v;
    if (v && this.isConnected && !this._initialized) this.init();
  }

  get query(): string { return this._query; }
  set query(v: string) {
    this._query = v;
    if (this.queryInput) {
      this.queryInput.value = v;
      if (this._initialized) this.doSearch();
    }
  }

  get hideBar(): boolean { return this._hideBar; }
  set hideBar(v: boolean) { this._hideBar = v; }

  get tag(): string { return this._tag; }
  set tag(v: string) {
    this._tag = v;
    this._baseTagName = v;
    // Play-button path: exploring "that one tag" — lock name to exact.
    this._baseTagNameMatch = "exact";
    if (this._initialized && v) {
      // R1408: pre-fill as structured tag-mode search
      this._searchMode = "tag";
      this.modeSelect.value = "tag";
      this.renderBarInputs();
    }
  }

  get value(): string { return this._value; }
  set value(v: string) {
    this._value = v;
    this._baseTagValue = v;
    if (this._initialized && this._tag) {
      this.renderBarInputs();
    }
  }

  connectedCallback(): void {
    if (this._api && !this._initialized) this.init();
  }

  disconnectedCallback(): void {
    if (this.lazyObserver) {
      this.lazyObserver.disconnect();
      this.lazyObserver = null;
    }
    if (this.heightListener) {
      window.removeEventListener("message", this.heightListener);
      this.heightListener = null;
    }
  }

  private init(): void {
    this._initialized = true;
    this.className = "ark-tag-search-panel";

    // Prevent CM from intercepting events inside the panel
    for (const evt of ["mousedown", "keydown", "keyup", "keypress", "focus", "input"] as const) {
      this.addEventListener(evt, (e) => e.stopPropagation());
    }

    // === Base query bar R1406 ===
    const bar = document.createElement("div");
    bar.className = "ark-search-bar";

    // Mode dropdown
    this.modeSelect = document.createElement("select");
    this.modeSelect.className = "ark-search-mode";
    for (const m of SEARCH_MODES) {
      const opt = document.createElement("option");
      opt.value = m;
      opt.textContent = m;
      this.modeSelect.appendChild(opt);
    }
    this.modeSelect.addEventListener("change", () => {
      this._searchMode = this.modeSelect.value as SearchMode;
      this.renderBarInputs();
      this.debouncedSearch();
    });

    // Swappable inputs area — structured tag fields OR single text input
    this.barInputs = document.createElement("div");
    this.barInputs.className = "ark-search-bar-inputs";

    // Persistent text-query input (attached only in non-tag modes)
    this.queryInput = document.createElement("input");
    this.queryInput.className = "ark-search-query";
    this.queryInput.type = "text";
    this.queryInput.placeholder = "search\u2026";
    this.queryInput.addEventListener("input", () => this.debouncedSearch());
    this.queryInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") this.doSearch();
    });

    // R1408: default mode — "tag" for interactive bar, "contains" for
    // bar-hidden code-block usage where the query is set programmatically.
    if (this._hideBar) {
      this._searchMode = "contains";
    } else if (this._tag) {
      this._searchMode = "tag";
      this._baseTagName = this._tag;
      this._baseTagValue = this._value;
    }

    this.modeSelect.value = this._searchMode;
    this.renderBarInputs();

    const clearBtn = document.createElement("button");
    clearBtn.className = "ark-search-clear";
    clearBtn.textContent = "\u00d7";
    clearBtn.title = "Clear query";
    clearBtn.addEventListener("click", () => {
      this.queryInput.value = "";
      this._baseTagName = "";
      this._baseTagValue = "";
      if (this._searchMode === "tag") this.renderBarInputs();
      this.phasedResults = [];
      this.clearResults();
    });

    const closeBtn = document.createElement("button");
    closeBtn.className = "ark-tag-search-close";
    closeBtn.textContent = "\u2715";
    closeBtn.title = "Close";
    closeBtn.addEventListener("click", () => {
      this.dispatchEvent(new CustomEvent("close", { bubbles: true }));
    });

    bar.appendChild(this.modeSelect);
    bar.appendChild(this.barInputs);
    bar.appendChild(clearBtn);
    bar.appendChild(closeBtn);

    // === Filter rows R1409 ===
    this.filtersContainer = document.createElement("div");
    this.filtersContainer.className = "ark-search-filters";

    const addBtn = document.createElement("button");
    addBtn.className = "ark-search-add-filter";
    addBtn.textContent = "+ add filter";
    addBtn.addEventListener("click", () => {
      const row = newFilterRow();
      this.filterGroups.push(newFilterGroup(row.polarity, [row]));
      this.renderFilterGroups();
    });

    // === Source-type bar R1419 ===
    this.sourceBar = document.createElement("div");
    this.sourceBar.className = "ark-search-source-bar";
    this.renderSourceBar();

    // === Chip bar for saved filter presets R1448 ===
    this.chipBar = document.createElement("div");
    this.chipBar.className = "ark-search-chip-bar";
    this.renderChipBar();

    // === Iframe lazy loading + auto-height ===
    this.lazyObserver = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const iframe = entry.target as HTMLIFrameElement;
        const src = iframe.dataset.src;
        if (src) {
          iframe.src = src;
          delete iframe.dataset.src;
          this.lazyObserver!.unobserve(iframe);
        }
      }
    }, { root: null, rootMargin: "200px" });

    this.heightListener = (e: MessageEvent) => {
      if (!e.data) return;
      // R1465: iframe page finished rendering — fade it in.
      if (e.data.type === "ark-content-ready") {
        const iframes = this.resultsEl.querySelectorAll<HTMLIFrameElement>("iframe.ark-search-chunk-iframe");
        for (const iframe of iframes) {
          if (iframe.contentWindow === e.source) {
            iframe.style.opacity = "1";
            break;
          }
        }
        return;
      }
      if (e.data.type !== "ark-content-height") return;
      const iframes = this.resultsEl.querySelectorAll<HTMLIFrameElement>("iframe.ark-search-chunk-iframe");
      for (const iframe of iframes) {
        if (iframe.contentWindow === e.source) {
          iframe.style.height = e.data.height + "px";
          break;
        }
      }
    };
    window.addEventListener("message", this.heightListener);

    // === Results area ===
    this.resultsEl = document.createElement("div");
    this.resultsEl.className = "ark-tag-search-results";

    // === Resize handle ===
    const resizeHandle = document.createElement("div");
    resizeHandle.className = "ark-tag-search-resize";
    let startY = 0, startH = 0;
    resizeHandle.addEventListener("mousedown", (e) => {
      startY = e.clientY;
      startH = this.resultsEl.offsetHeight;
      const onMove = (ev: MouseEvent) => {
        this.resultsEl.style.height = Math.max(100, startH + ev.clientY - startY) + "px";
      };
      const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });

    // R1408: pre-fill from query property if set
    if (this._query) {
      this.queryInput.value = this._query;
    }

    if (!this._hideBar) {
      this.appendChild(bar);
      this.appendChild(this.filtersContainer);
      this.appendChild(addBtn);
      this.appendChild(this.sourceBar);
      this.appendChild(this.chipBar);
    }
    this.appendChild(this.resultsEl);
    if (!this._hideBar) {
      this.appendChild(resizeHandle);
    }

    // Initial search — tag mode uses structured fields; other modes use queryInput.
    const hasInitialQuery = this._searchMode === "tag"
      ? this._baseTagName.trim().length > 0
      : this.queryInput.value.length > 0;
    if (hasInitialQuery) {
      const loading = document.createElement("div");
      loading.className = "ark-tag-search-loading";
      loading.textContent = "Searching\u2026";
      this.clearResults(loading);
      this.doSearch();
    }
    if (!this._hideBar) {
      setTimeout(() => {
        const first = this.barInputs.querySelector<HTMLInputElement>("input");
        (first || this.queryInput).focus();
      }, 0);
    }
  }

  // === Filter Row Rendering R1410-R1415 ===

  /** Flatten all groups into individual rows (for collection methods). */
  private allFilterRows(): FilterRow[] {
    return this.filterGroups.flatMap(g => g.rows);
  }

  private renderFilterGroups(): void {
    this.filtersContainer.innerHTML = "";
    for (const group of this.filterGroups) {
      if (group.rows.length === 1) {
        // Single row — render flat with expand button
        this.filtersContainer.appendChild(this.createFilterRowEl(group.rows[0], group));
      } else {
        // OR group — visual grouping R1442
        const groupEl = document.createElement("div");
        groupEl.className = "ark-search-or-group";

        const label = document.createElement("span");
        label.className = "ark-search-or-label";
        label.textContent = "OR";
        groupEl.appendChild(label);

        for (const row of group.rows) {
          groupEl.appendChild(this.createFilterRowEl(row, group));
        }
        this.filtersContainer.appendChild(groupEl);
      }
    }
    this.updateSourceBarState();
  }

  private createFilterRowEl(row: FilterRow, group: FilterGroup): HTMLElement {
    const el = document.createElement("div");
    el.className = "ark-search-filter-row";

    // Polarity: with/without
    const polaritySel = document.createElement("select");
    polaritySel.className = "ark-search-filter-polarity";
    for (const p of ["with", "without"] as const) {
      const opt = document.createElement("option");
      opt.value = p;
      opt.textContent = p;
      opt.selected = p === row.polarity;
      polaritySel.appendChild(opt);
    }
    polaritySel.addEventListener("change", () => {
      row.polarity = polaritySel.value as Polarity;
      this.debouncedSearch();
    });

    // Mode
    const modeSel = document.createElement("select");
    modeSel.className = "ark-search-filter-mode";
    for (const m of FILTER_MODES) {
      const opt = document.createElement("option");
      opt.value = m;
      opt.textContent = m;
      opt.selected = m === row.mode;
      modeSel.appendChild(opt);
    }
    modeSel.addEventListener("change", () => {
      row.mode = modeSel.value as FilterMode;
      this.renderFilterGroups(); // re-render to switch input type
      this.debouncedSearch();
    });

    el.appendChild(polaritySel);
    el.appendChild(modeSel);

    // Mode-specific inputs
    if (row.mode === "tag") {
      // R1413: structured tag fields
      const at = document.createElement("span");
      at.className = "ark-search-filter-at";
      at.textContent = "@";

      // Name match toggle (contains ~ / exact =)
      const nameMatchBtn = document.createElement("button");
      nameMatchBtn.className = "ark-search-filter-tag-name-match";
      nameMatchBtn.textContent = TAG_NAME_MATCH_LABELS[row.tagNameMatch];
      nameMatchBtn.title = `Name match: ${row.tagNameMatch}`;
      nameMatchBtn.addEventListener("click", () => {
        const idx = TAG_NAME_MATCH_CYCLE.indexOf(row.tagNameMatch);
        row.tagNameMatch = TAG_NAME_MATCH_CYCLE[(idx + 1) % TAG_NAME_MATCH_CYCLE.length];
        nameMatchBtn.textContent = TAG_NAME_MATCH_LABELS[row.tagNameMatch];
        nameMatchBtn.title = `Name match: ${row.tagNameMatch}`;
        this.debouncedSearch();
      });

      const nameInput = document.createElement("input");
      nameInput.className = "ark-search-filter-tag-name";
      nameInput.type = "text";
      nameInput.value = row.tagName;
      nameInput.placeholder = "tag";
      nameInput.size = Math.max(row.tagName.length + 2, 8);
      nameInput.addEventListener("input", () => {
        row.tagName = nameInput.value;
        nameInput.size = Math.max(nameInput.value.length + 2, 8);
        this.debouncedSearch();
      });
      nameInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.doSearch();
      });

      const colon = document.createElement("span");
      colon.textContent = ": ";

      const matchBtn = document.createElement("button");
      matchBtn.className = "ark-search-filter-tag-match";
      matchBtn.textContent = TAG_MATCH_LABELS[row.tagMatchMode];
      matchBtn.title = `Value match: ${row.tagMatchMode}`;
      matchBtn.addEventListener("click", () => {
        const idx = TAG_MATCH_CYCLE.indexOf(row.tagMatchMode);
        row.tagMatchMode = TAG_MATCH_CYCLE[(idx + 1) % TAG_MATCH_CYCLE.length];
        matchBtn.textContent = TAG_MATCH_LABELS[row.tagMatchMode];
        matchBtn.title = `Value match: ${row.tagMatchMode}`;
        this.debouncedSearch();
      });

      const valInput = document.createElement("input");
      valInput.className = "ark-search-filter-tag-value";
      valInput.type = "text";
      valInput.value = row.tagValue;
      valInput.placeholder = "value...";
      valInput.addEventListener("input", () => {
        row.tagValue = valInput.value;
        this.debouncedSearch();
      });
      valInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.doSearch();
      });

      el.appendChild(at);
      el.appendChild(nameMatchBtn);
      el.appendChild(nameInput);
      el.appendChild(colon);
      el.appendChild(matchBtn);
      el.appendChild(valInput);
    } else {
      // R1412, R1415: free text or glob input
      const input = document.createElement("input");
      input.className = "ark-search-filter-query";
      input.type = "text";
      input.value = row.query;
      input.placeholder = row.mode === "files" ? "*.md, **/*.jsonl" : "filter...";
      input.addEventListener("input", () => {
        row.query = input.value;
        this.debouncedSearch();
      });
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.doSearch();
      });
      el.appendChild(input);
    }

    // Expand button R1433-R1436
    const canExpand = (row.mode === "tag" || row.mode === "fuzzy") && this._api?.embedMatch;
    if (canExpand && group.rows.length === 1) {
      const expandBtn = document.createElement("button");
      expandBtn.className = "ark-search-filter-expand";
      expandBtn.textContent = "\u21bb";
      expandBtn.title = "Expand to OR group";
      expandBtn.addEventListener("click", () => this.expandRow(row, group));
      el.appendChild(expandBtn);
    }

    // Remove button R1440-R1441
    const removeBtn = document.createElement("button");
    removeBtn.className = "ark-search-filter-remove";
    removeBtn.textContent = "\u00d7";
    removeBtn.title = "Remove filter";
    removeBtn.addEventListener("click", () => {
      group.rows = group.rows.filter(r => r.id !== row.id);
      if (group.rows.length === 0) {
        this.filterGroups = this.filterGroups.filter(g => g.id !== group.id);
      }
      this.renderFilterGroups();
      this.debouncedSearch();
    });
    el.appendChild(removeBtn);

    return el;
  }

  // === Source-Type Bar R1419-R1422 ===

  private renderSourceBar(): void {
    this.sourceBar.innerHTML = "";
    for (const src of this.sourceToggles) {
      const btn = document.createElement("button");
      btn.className = `ark-search-source-toggle${src.active ? " active" : ""}`;
      btn.textContent = src.name;
      btn.addEventListener("click", () => {
        src.active = !src.active;
        btn.classList.toggle("active", src.active);
        this.debouncedSearch();
      });
      this.sourceBar.appendChild(btn);
    }
  }

  // === Chip Bar R1448-R1453 ===

  private renderChipBar(): void {
    this.chipBar.innerHTML = "";

    // [+ save] button R1449
    const saveBtn = document.createElement("button");
    saveBtn.className = "ark-search-chip-save";
    saveBtn.textContent = "+ save";
    saveBtn.addEventListener("click", () => {
      const name = prompt("Filter preset name:");
      if (!name?.trim()) return;
      const presets = loadPresets();
      presets[name.trim()] = { groups: this.filterGroups };
      savePresets(presets);
      this.renderChipBar();
    });
    this.chipBar.appendChild(saveBtn);

    // Saved chips R1450-R1451
    const presets = loadPresets();
    const entries = Object.entries(presets);
    const MAX_VISIBLE = 3;
    const visible = entries.slice(0, MAX_VISIBLE);
    const overflow = entries.slice(MAX_VISIBLE);

    const makeChip = (name: string, preset: FilterPreset): HTMLElement => {
      const chip = document.createElement("span");
      chip.className = "ark-search-chip";

      const label = document.createElement("span");
      label.className = "ark-search-chip-label";
      label.textContent = name;
      label.title = `Load "${name}" filters`;
      label.addEventListener("click", () => {
        this.filterGroups = preset.groups.map(g => ({
          ...g,
          id: nextFilterId++,
          rows: g.rows.map(r => ({
            ...r,
            // Older presets may lack tagNameMatch — default to contains.
            tagNameMatch: (r.tagNameMatch ?? "contains") as TagNameMatch,
            id: nextFilterId++,
          })),
        }));
        this.renderFilterGroups();
        this.debouncedSearch();
      });

      const removeBtn = document.createElement("button");
      removeBtn.className = "ark-search-chip-remove";
      removeBtn.textContent = "\u00d7";
      removeBtn.title = `Remove "${name}"`;
      removeBtn.addEventListener("click", (e) => {
        e.stopPropagation();
        const p = loadPresets();
        delete p[name];
        savePresets(p);
        this.renderChipBar();
      });

      chip.appendChild(label);
      chip.appendChild(removeBtn);
      return chip;
    };

    for (const [name, preset] of visible) {
      this.chipBar.appendChild(makeChip(name, preset));
    }

    // Overflow dropdown
    if (overflow.length > 0) {
      const moreBtn = document.createElement("button");
      moreBtn.className = "ark-search-chip-more";
      moreBtn.textContent = "\u2026";
      moreBtn.title = `${overflow.length} more saved filters`;

      const dropdown = document.createElement("div");
      dropdown.className = "ark-search-chip-dropdown";
      dropdown.style.display = "none";
      for (const [name, preset] of overflow) {
        dropdown.appendChild(makeChip(name, preset));
      }

      moreBtn.addEventListener("click", () => {
        const show = dropdown.style.display === "none";
        dropdown.style.display = show ? "flex" : "none";
        moreBtn.classList.toggle("active", show);
      });

      this.chipBar.appendChild(moreBtn);
      this.chipBar.appendChild(dropdown);
    }
  }

  // === Base Query Bar Rendering (tag mode inputs) R1408 ===

  private renderBarInputs(): void {
    this.barInputs.innerHTML = "";

    if (this._searchMode === "tag") {
      const at = document.createElement("span");
      at.className = "ark-search-bar-at";
      at.textContent = "@";

      const nameInput = document.createElement("input");
      nameInput.className = "ark-search-bar-tag-name";
      nameInput.type = "text";
      nameInput.value = this._baseTagName;
      nameInput.placeholder = "tag";
      nameInput.size = Math.max(this._baseTagName.length + 2, 8);
      nameInput.addEventListener("input", () => {
        this._baseTagName = nameInput.value;
        nameInput.size = Math.max(nameInput.value.length + 2, 8);
        this.debouncedSearch();
      });
      nameInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.doSearch();
      });

      // Name match toggle (contains ~ / exact =). Default contains for
      // user-typed queries; set tag() flips it to exact on play-button open.
      const nameMatchBtn = document.createElement("button");
      nameMatchBtn.className = "ark-search-bar-tag-name-match";
      nameMatchBtn.textContent = TAG_NAME_MATCH_LABELS[this._baseTagNameMatch];
      nameMatchBtn.title = `Name match: ${this._baseTagNameMatch}`;
      nameMatchBtn.addEventListener("click", () => {
        const idx = TAG_NAME_MATCH_CYCLE.indexOf(this._baseTagNameMatch);
        this._baseTagNameMatch = TAG_NAME_MATCH_CYCLE[(idx + 1) % TAG_NAME_MATCH_CYCLE.length];
        nameMatchBtn.textContent = TAG_NAME_MATCH_LABELS[this._baseTagNameMatch];
        nameMatchBtn.title = `Name match: ${this._baseTagNameMatch}`;
        this.debouncedSearch();
      });

      const colon = document.createElement("span");
      colon.className = "ark-search-bar-colon";
      colon.textContent = ":";

      const matchBtn = document.createElement("button");
      matchBtn.className = "ark-search-bar-tag-match";
      matchBtn.textContent = TAG_MATCH_LABELS[this._baseTagMatch];
      matchBtn.title = `Value match: ${this._baseTagMatch}`;
      matchBtn.addEventListener("click", () => {
        const idx = TAG_MATCH_CYCLE.indexOf(this._baseTagMatch);
        this._baseTagMatch = TAG_MATCH_CYCLE[(idx + 1) % TAG_MATCH_CYCLE.length];
        matchBtn.textContent = TAG_MATCH_LABELS[this._baseTagMatch];
        matchBtn.title = `Value match: ${this._baseTagMatch}`;
        this.debouncedSearch();
      });

      const valInput = document.createElement("input");
      valInput.className = "ark-search-bar-tag-value";
      valInput.type = "text";
      valInput.value = this._baseTagValue;
      valInput.placeholder = "value\u2026";
      valInput.addEventListener("input", () => {
        this._baseTagValue = valInput.value;
        this.debouncedSearch();
      });
      valInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") this.doSearch();
      });

      this.barInputs.appendChild(at);
      this.barInputs.appendChild(nameMatchBtn);
      this.barInputs.appendChild(nameInput);
      this.barInputs.appendChild(colon);
      this.barInputs.appendChild(matchBtn);
      this.barInputs.appendChild(valInput);
    } else {
      this.barInputs.appendChild(this.queryInput);
    }
  }

  /** Translate base tag-mode state into a regex query (server has no
   *  tag mode on /search/grouped). Name respects contains/exact; value
   *  is tokenized and OR'd so word order doesn't matter. */
  /** Regex patterns to highlight inside iframe chunk previews. For
   *  tag mode: one regex for the name prefix plus one per value token
   *  (so each token lights up separately). For text modes: the escaped
   *  query or raw regex. Fuzzy returns [] — no clean regex translation. */
  private buildHighlightRegexes(): string[] {
    if (this._searchMode === "tag") {
      const rawName = this._baseTagName.trim();
      if (!rawName) return [];
      const prefix = tagPrefixRegex(rawName, this._baseTagNameMatch);
      const rawVal = this._baseTagValue.trim();
      if (!rawVal) return [prefix];
      // Anchor value tokens to the tag line using a capture group.
      // The full match includes the tag prefix (for anchoring), but
      // the highlight extension only decorates group 1 (the token).
      if (this._baseTagMatch === "regex") {
        return [prefix, `${prefix}[^\\n]*?(${rawVal})`];
      }
      return [prefix, ...tokenizeValue(rawVal).map(
        tok => `${prefix}[^\\n]*?(${escapeRegex(tok)})`
      )];
    }

    const raw = this.queryInput.value.trim();
    if (!raw) return [];
    if (this._searchMode === "regex") return [raw];
    if (this._searchMode === "fuzzy") return [];
    return [escapeRegex(raw)];
  }

  /** R1421-R1422: gray out source bar when user has files filter rows. */
  private updateSourceBarState(): void {
    const hasFileRows = this.allFilterRows().some(r => r.mode === "files" && r.query.trim());
    this.sourceBar.classList.toggle("ark-search-source-overridden", hasFileRows);
  }

  // === Query Expansion R1434-R1435 ===

  private expandRow(row: FilterRow, group: FilterGroup): void {
    const api = this._api;
    if (!api?.embedMatch) return;

    const query = row.mode === "tag"
      ? (row.tagValue ? `${row.tagName} ${row.tagValue}` : row.tagName)
      : row.query;

    if (!query.trim()) return;

    api.embedMatch(query).then((matches) => {
      if (matches.length === 0) return;

      // Replace the original row with concrete exact-match rows R1435
      const newRows: FilterRow[] = matches.map(m => {
        const r = newFilterRow(row.mode, group.polarity);
        if (row.mode === "tag") {
          r.tagName = m.tag;
          r.tagValue = m.value;
          r.tagMatchMode = "exact";
          r.tagNameMatch = "exact";
        } else {
          r.query = `${m.tag}: ${m.value}`;
        }
        return r;
      });

      group.rows = newRows;
      this.renderFilterGroups();
      this.debouncedSearch();
    });
  }

  // === Search Execution ===

  private debouncedSearch(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    this.debounceTimer = setTimeout(() => this.doSearch(), 300);
  }

  /** Collect file filters from filter rows + source bar. R1418-R1422 */
  private collectFileFilters(): { filterFiles: string[]; excludeFiles: string[] } {
    const filterFiles: string[] = [];
    const excludeFiles: string[] = [];

    // R1421: if user has files rows, they replace source bar entirely
    const fileRows = this.allFilterRows().filter(r => r.mode === "files" && r.query.trim());
    if (fileRows.length > 0) {
      for (const row of fileRows) {
        const patterns = row.query.split(",").map(s => s.trim()).filter(Boolean);
        if (row.polarity === "with") {
          filterFiles.push(...patterns);
        } else {
          excludeFiles.push(...patterns);
        }
      }
    } else {
      // Source-type bar active: inactive sources become exclude patterns
      for (const src of this.sourceToggles) {
        if (!src.active && src.pattern) {
          excludeFiles.push(src.pattern);
        }
      }
    }
    return { filterFiles, excludeFiles };
  }

  /** Build a regex for a single filter tag row. Used for OR group
   *  serialization (sent to server as regex chunk filter — must be
   *  RE2-compatible, no lookaheads). Value tokens are OR'd here since
   *  RE2 can't express AND; the T/V record path handles AND precision
   *  for single-row filters. */
  private tagRowRegex(row: FilterRow): string {
    const prefix = tagPrefixRegex(row.tagName.trim(), row.tagNameMatch);
    const rawVal = row.tagValue.trim();
    if (!rawVal) return prefix;
    if (row.tagMatchMode === "regex") return `${prefix}\\s*${rawVal}`;
    const tokens = tokenizeValue(rawVal).map(escapeRegex);
    if (tokens.length === 0) return prefix;
    const alt = tokens.length === 1 ? tokens[0] : `(?:${tokens.join("|")})`;
    return `${prefix}[^\\n]*${alt}`;
  }

  /** Collect chunk-level filters from filter groups. R1416-R1417, R1443-R1446 */
  private collectChunkFilters(): ChunkFilterParam[] {
    const filters: ChunkFilterParam[] = [];
    for (const group of this.filterGroups) {
      const rows = group.rows.filter(r => r.mode !== "files");
      if (rows.length === 0) continue;

      if (rows.length === 1) {
        // Single row — send as-is
        const row = rows[0];
        if (row.mode === "tag") {
          if (!row.tagName.trim()) continue;
          // R1473: contains-name uses tag-contains mode for server-side
          // T/V record resolution. Exact-name stays on the fast tag path.
          if (row.tagNameMatch === "contains") {
            const nameTokens = row.tagName.trim().split(/\s+/).filter(Boolean);
            const valueTokens = row.tagValue.trim().split(/\s+/).filter(Boolean);
            const q = valueTokens.length > 0
              ? `${nameTokens.join(" ")}:${valueTokens.join(" ")}`
              : nameTokens.join(" ");
            filters.push({
              polarity: group.polarity,
              mode: "tag-contains",
              query: q,
            });
          } else {
            const q = row.tagValue.trim()
              ? `${row.tagName.trim()}:${row.tagValue.trim()}`
              : row.tagName.trim();
            filters.push({ polarity: group.polarity, mode: "tag", query: q });
          }
        } else {
          if (!row.query.trim()) continue;
          filters.push({
            polarity: group.polarity,
            mode: row.mode as "contains" | "fuzzy" | "regex",
            query: row.query.trim(),
          });
        }
      } else {
        // OR group — serialize as regex R1443-R1445
        const alts: string[] = [];
        for (const row of rows) {
          if (row.mode === "tag") {
            if (!row.tagName.trim()) continue;
            alts.push(this.tagRowRegex(row));
          } else {
            if (!row.query.trim()) continue;
            alts.push(escapeRegex(row.query.trim()));
          }
        }
        if (alts.length === 0) continue;
        const regex = alts.length === 1 ? alts[0] : `(${alts.join("|")})`;
        filters.push({ polarity: group.polarity, mode: "regex", query: regex });
      }
    }
    return filters;
  }

  private doSearch(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    this.debounceTimer = null;
    this.updateSourceBarState();

    const api = this._api;
    if (!api) return;

    // R1408, R1472: in tag mode, send structured tokens for server-side
    // T/V record resolution. Both exact and contains name modes use the
    // same path — the server handles both via MatchTagNames/MatchTagValues.
    let query: string;
    let mode: string;
    let nameTokens: string[] | undefined;
    let valueTokens: string[] | undefined;
    let nameMatch: "exact" | "contains" | undefined;
    if (this._searchMode === "tag") {
      if (!this._baseTagName.trim()) {
        this.phasedResults = [];
        this.clearResults();
        return;
      }
      nameTokens = this._baseTagName.trim().split(/\s+/).filter(Boolean);
      nameMatch = this._baseTagNameMatch;
      const rawVal = this._baseTagValue.trim();
      if (rawVal) valueTokens = rawVal.split(/\s+/).filter(Boolean);
      query = ""; // server builds the query from tokens
      mode = "regex"; // result is a regex search
    } else {
      query = this.queryInput.value.trim();
      if (!query) {
        this.phasedResults = [];
        this.clearResults();
        return;
      }
      mode = this._searchMode;
    }

    const gen = ++this.searchGeneration;
    this.phasedResults = [];

    const chunkFilters = this.collectChunkFilters();
    const { filterFiles, excludeFiles } = this.collectFileFilters();
    const hasFilters = chunkFilters.length > 0 || filterFiles.length > 0 || excludeFiles.length > 0 || !!nameTokens;

    // Phase 1: trigram search R1386
    let phase1: Promise<void>;
    if (hasFilters && api.searchFiltered) {
      phase1 = api.searchFiltered(query, {
        mode,
        chunkFilters: chunkFilters.length > 0 ? chunkFilters : undefined,
        filterFiles: filterFiles.length > 0 ? filterFiles : undefined,
        excludeFiles: excludeFiles.length > 0 ? excludeFiles : undefined,
        nameTokens,
        valueTokens,
        nameMatch,
      }).then((groups) => {
        if (gen !== this.searchGeneration) return;
        for (const g of groups) {
          this.phasedResults.push({ group: g, phase: "trigram" });
        }
        this.renderResults();
      });
    } else {
      phase1 = api.search(query, mode).then((groups) => {
        if (gen !== this.searchGeneration) return;
        for (const g of groups) {
          this.phasedResults.push({ group: g, phase: "trigram" });
        }
        this.renderResults();
      });
    }

    // Phase 2: embedding (if available) R1387
    const hasPhase2 = api.embedMatch && api.expandSearch;
    let phase2Matches: TagMatch[] | null = null;

    if (hasPhase2) {
      api.embedMatch!(query).then((matches) => {
        if (gen !== this.searchGeneration || matches.length === 0) return;
        phase2Matches = matches;
        const alts = matches.map(m => ({ tag: m.tag, value: m.value }));
        return api.expandSearch!(alts);
      }).then((groups) => {
        if (gen !== this.searchGeneration || !groups) return;
        const phase1Paths = new Set(
          this.phasedResults
            .filter(p => p.phase === "trigram")
            .map(p => p.group.path)
        );
        for (const g of groups) {
          if (!phase1Paths.has(g.path)) {
            this.phasedResults.push({ group: g, phase: "candidate" });
          }
        }
        this.renderResults();

        if (api.curateRequest && api.curateResult && phase2Matches) {
          const tag = this._tag || query;
          const value = this._value || "";
          this.startCuration(api, gen, tag, value, phase2Matches);
        }
      });
    }

    // Show empty state after phase 1
    phase1.then(() => {
      if (gen !== this.searchGeneration) return;
      if (this.phasedResults.length === 0) {
        const empty = document.createElement("div");
        empty.className = "ark-tag-search-empty";
        empty.textContent = "No results";
        this.clearResults(empty);
      }
    });
  }

  // === Phase 3: Curation R1388 ===

  private startCuration(
    api: SearchAPI, gen: number, tag: string, value: string, candidates: TagMatch[],
  ): void {
    api.curateRequest!(tag, value, candidates).then((requestId) => {
      if (gen !== this.searchGeneration) return;
      this.pollCuration(api, gen, requestId);
    });
  }

  private pollCuration(api: SearchAPI, gen: number, requestId: string): void {
    api.curateResult!(requestId).then((result) => {
      if (gen !== this.searchGeneration) return;
      if (!result.done) {
        setTimeout(() => this.pollCuration(api, gen, requestId), 500);
        return;
      }
      if (result.error) return;

      const curatedKeys = new Set(result.curated.map(m => `${m.tag}\0${m.value}`));
      const rejectedKeys = new Set(result.rejected.map(m => `${m.tag}\0${m.value}`));

      for (const pr of this.phasedResults) {
        if (pr.phase !== "candidate") continue;
        const key = this.matchGroupToTag(pr);
        if (key && curatedKeys.has(key)) {
          pr.phase = "curated";
        } else if (key && rejectedKeys.has(key)) {
          pr.phase = "rejected";
        }
      }
      this.renderResults();
    });
  }

  private matchGroupToTag(pr: PhasedGroup): string | null {
    void pr;
    return null;
  }

  // === Result Rendering R1364-R1367, R1392-R1394, R1464-R1465 ===

  /** Drop the result cache and replace the results DOM with the given
   *  content (or nothing). Use this wherever results need to go away —
   *  never touch `resultsEl.innerHTML` directly, or the cache will
   *  point at detached nodes. */
  private clearResults(placeholder: Node | null = null): void {
    this.resultEls.clear();
    if (placeholder) this.resultsEl.replaceChildren(placeholder);
    else this.resultsEl.replaceChildren();
  }

  /** Signature of the chunk layout alone — if this changes we must
   *  rebuild the DOM (new iframes with new range params). */
  private chunkSignature(group: SearchResultGroup): string {
    return group.chunks.map(c => c.range || "").join("|");
  }

  /** Signature of the highlight regex list — if only this changes we
   *  can update iframes in place via postMessage (R1466). */
  private highlightSignature(highlights: string[]): string {
    return highlights.join("||");
  }

  /** Push an updated highlight list into every iframe of an existing
   *  group element, without reloading. Loaded iframes receive a
   *  postMessage; iframes still waiting on lazy load get their
   *  `dataset.src` URL rewritten so they load with the fresh params. */
  private updateGroupHighlights(groupEl: HTMLElement, highlights: string[]): void {
    const iframes = groupEl.querySelectorAll<HTMLIFrameElement>("iframe.ark-search-chunk-iframe");
    for (const iframe of iframes) {
      if (iframe.dataset.src) {
        // Not yet loaded by the lazy observer — rewrite the pending URL.
        try {
          const url = new URL(iframe.dataset.src, location.origin);
          url.searchParams.delete("highlight");
          for (const h of highlights) url.searchParams.append("highlight", h);
          // Preserve the path-relative form the rest of the code uses.
          iframe.dataset.src = url.pathname + url.search;
        } catch {
          // Malformed URL — leave it alone.
        }
      } else if (iframe.contentWindow) {
        // Loaded — swap highlights live without reload. R1466
        iframe.contentWindow.postMessage(
          { type: "ark-set-highlights", patterns: highlights },
          "*",
        );
      }
    }
  }

  /** Build a single group element from scratch. Used for both new
   *  paths and signature-changed paths (where reuse isn't safe). */
  private buildGroupEl(
    group: SearchResultGroup,
    phase: Phase,
    highlights: string[],
    api: SearchAPI,
  ): HTMLElement {
    const groupEl = document.createElement("div");
    groupEl.className = `ark-tag-search-group ark-search-phase-${phase}`;

    const header = document.createElement("div");
    header.className = "ark-tag-search-group-header";

    const pathLink = document.createElement("a");
    pathLink.className = "ark-tag-search-path";
    pathLink.textContent = group.path;
    pathLink.href = "/content" + group.path;
    pathLink.addEventListener("click", (e) => {
      e.preventDefault();
      api.navigate(group.path);
    });
    header.appendChild(pathLink);

    if (phase === "candidate") {
      const badge = document.createElement("span");
      badge.className = "ark-search-candidate-badge";
      badge.textContent = "candidate";
      header.appendChild(badge);
    }

    if (api.showInFolder) {
      const folderBtn = document.createElement("button");
      folderBtn.className = "ark-tag-search-folder";
      folderBtn.innerHTML = "&#128193;";
      folderBtn.title = "Show in file manager";
      folderBtn.addEventListener("click", () => api.showInFolder!(group.path));
      header.appendChild(folderBtn);
    }

    groupEl.appendChild(header);

    for (const chunk of group.chunks) {
      const chunkEl = document.createElement("div");
      chunkEl.className = "ark-tag-search-chunk";

      // Iframe preview: /content/ with range, edit, toggle, highlight params
      const params = new URLSearchParams();
      if (chunk.range) params.set("range", chunk.range);
      params.set("edit", "true");
      params.set("toggle", "false");
      for (const h of highlights) params.append("highlight", h);
      const iframeSrc = `/content${group.path}?${params}`;

      const iframe = document.createElement("iframe");
      iframe.className = "ark-search-chunk-iframe";
      iframe.style.width = "100%";
      iframe.style.height = "150px"; // initial, resized by postMessage
      iframe.style.border = "none";
      iframe.style.overflow = "hidden";
      iframe.style.opacity = "0"; // fades in on ark-content-ready — R1465
      iframe.dataset.src = iframeSrc; // lazy load
      iframe.title = `${group.path} ${chunk.range}`;

      if (this.lazyObserver) {
        this.lazyObserver.observe(iframe);
      }

      chunkEl.appendChild(iframe);
      groupEl.appendChild(chunkEl);
    }

    return groupEl;
  }

  /** Update only the phase class and candidate badge of an existing
   *  group element — used when the phase changes but everything else
   *  (chunks, highlights) is unchanged. Leaves iframes untouched. */
  private updateGroupPhase(groupEl: HTMLElement, phase: Phase): void {
    groupEl.className = `ark-tag-search-group ark-search-phase-${phase}`;
    const header = groupEl.querySelector(".ark-tag-search-group-header");
    if (!header) return;
    const existingBadge = header.querySelector(".ark-search-candidate-badge");
    if (phase === "candidate" && !existingBadge) {
      const badge = document.createElement("span");
      badge.className = "ark-search-candidate-badge";
      badge.textContent = "candidate";
      const folder = header.querySelector(".ark-tag-search-folder");
      header.insertBefore(badge, folder || null);
    } else if (phase !== "candidate" && existingBadge) {
      existingBadge.remove();
    }
  }

  private renderResults(): void {
    if (this.phasedResults.length === 0) {
      this.clearResults();
      return;
    }

    const api = this._api!;
    const highlights = this.buildHighlightRegexes();
    const highlightSig = this.highlightSignature(highlights);
    const seen = new Set<string>();
    const desired: HTMLElement[] = [];

    // Pass 1: reuse or (re)build group elements, keyed by path.
    // First-seen phase wins for a path — later phases of the same
    // path (e.g. a candidate duplicate) are skipped.
    for (const { group, phase } of this.phasedResults) {
      if (seen.has(group.path)) continue;
      seen.add(group.path);

      const chunkSig = this.chunkSignature(group);
      let entry = this.resultEls.get(group.path);

      if (!entry) {
        // New path — build from scratch.
        const el = this.buildGroupEl(group, phase, highlights, api);
        entry = { el, chunkSig, highlightSig, phase };
        this.resultEls.set(group.path, entry);
      } else if (entry.chunkSig !== chunkSig) {
        // Chunks differ — must rebuild to get new iframe URLs.
        const newEl = this.buildGroupEl(group, phase, highlights, api);
        entry.el.replaceWith(newEl);
        entry.el = newEl;
        entry.chunkSig = chunkSig;
        entry.highlightSig = highlightSig;
        entry.phase = phase;
      } else {
        // Same chunks. Handle highlight and phase updates in place.
        if (entry.highlightSig !== highlightSig) {
          this.updateGroupHighlights(entry.el, highlights);
          entry.highlightSig = highlightSig;
        }
        if (entry.phase !== phase) {
          this.updateGroupPhase(entry.el, phase);
          entry.phase = phase;
        }
      }
      desired.push(entry.el);
    }

    // Pass 2: drop cached entries whose paths are no longer present.
    for (const [path, entry] of this.resultEls) {
      if (!seen.has(path)) {
        entry.el.remove();
        this.resultEls.delete(path);
      }
    }

    // Pass 3: drop any untracked children (loading / empty placeholders
    // left over from previous states) so they don't stick around next to
    // the real results.
    const tracked = new Set<Element>(desired);
    for (let i = this.resultsEl.children.length - 1; i >= 0; i--) {
      const child = this.resultsEl.children[i];
      if (!tracked.has(child)) child.remove();
    }

    // Pass 4: reorder in place. insertBefore on an existing child of
    // the same parent moves it without detaching — iframes keep their
    // contentWindow and are not reloaded.
    for (let i = 0; i < desired.length; i++) {
      const el = desired[i];
      if (this.resultsEl.children[i] !== el) {
        this.resultsEl.insertBefore(el, this.resultsEl.children[i] || null);
      }
    }
  }
}

// Register the custom element
customElements.define("ark-search", ArkSearchElement);
