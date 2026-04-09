// CRC: crc-ArkSearchElement.md | Seq: seq-tag-click.md
// R1356-R1367, R1372, R1373, R1377, R1386-R1394

import type { SearchAPI, SearchResultGroup, TagMatch } from "./search-api";

/** Source phase for visual treatment. */
type Phase = "trigram" | "candidate" | "curated" | "rejected";

/** A result group tagged with its source phase. */
interface PhasedGroup {
  group: SearchResultGroup;
  phase: Phase;
}

/** Validate tag name: must match @tag: pattern. */
const validTagName = /^[a-zA-Z][\w.-]*$/;

/** Escape string for use in regex. */
function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * `<ark-search>` custom element — standalone tag search panel
 * with three-phase progressive search.
 *
 * Properties (set by host after creation):
 * - api: SearchAPI — required
 * - tag: string — initial tag name
 * - value: string — initial value filter
 *
 * Dispatches 'close' CustomEvent when the close button is clicked.
 */
export class ArkSearchElement extends HTMLElement {
  private _api: SearchAPI | null = null;
  private _tag = "";
  private _value = "";
  private _initialized = false;

  // DOM refs
  private tagInput!: HTMLInputElement;
  private valueInput!: HTMLInputElement;
  private regexBtn!: HTMLButtonElement;
  private resultsEl!: HTMLElement;
  private useRegex = false;
  private debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Progressive search state
  private searchGeneration = 0; // increments on each search, stale results ignored
  private phasedResults: PhasedGroup[] = [];

  get api(): SearchAPI | null { return this._api; }
  set api(v: SearchAPI | null) {
    this._api = v;
    if (v && this.isConnected && !this._initialized) this.init();
  }

  get tag(): string { return this._tag; }
  set tag(v: string) {
    this._tag = v;
    if (this.tagInput) {
      this.tagInput.value = v;
      this.tagInput.size = Math.max(v.length + 2, 8);
    }
  }

  get value(): string { return this._value; }
  set value(v: string) {
    this._value = v;
    if (this.valueInput) this.valueInput.value = v;
  }

  connectedCallback(): void {
    if (this._api && !this._initialized) this.init();
  }

  private init(): void {
    this._initialized = true;
    this.className = "ark-tag-search-panel";

    // Prevent CM from intercepting events inside the panel
    for (const evt of ["mousedown", "keydown", "keyup", "keypress", "focus", "input"] as const) {
      this.addEventListener(evt, (e) => e.stopPropagation());
    }

    // Query bar
    const bar = document.createElement("div");
    bar.className = "ark-tag-search-bar";

    const atSign = document.createElement("span");
    atSign.className = "ark-tag-search-at";
    atSign.textContent = "@";

    this.tagInput = document.createElement("input");
    this.tagInput.className = "ark-tag-search-tag";
    this.tagInput.type = "text";
    this.tagInput.value = this._tag;
    this.tagInput.placeholder = "tag";
    this.tagInput.size = Math.max(this._tag.length + 2, 8);
    this.tagInput.addEventListener("input", () => {
      this.tagInput.size = Math.max(this.tagInput.value.length + 2, 8);
      this.debouncedSearch();
    });
    this.tagInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") this.doSearch();
    });

    const colonSpace = document.createElement("span");
    colonSpace.className = "ark-tag-search-colon";
    colonSpace.textContent = ": ";

    this.regexBtn = document.createElement("button");
    this.regexBtn.className = "ark-tag-search-regex";
    this.regexBtn.textContent = "Aa";
    this.regexBtn.title = "Toggle regex mode";
    this.regexBtn.addEventListener("click", () => {
      this.useRegex = !this.useRegex;
      this.regexBtn.textContent = this.useRegex ? ".*" : "Aa";
      this.regexBtn.classList.toggle("active", this.useRegex);
      this.doSearch();
    });

    this.valueInput = document.createElement("input");
    this.valueInput.className = "ark-tag-search-value";
    this.valueInput.type = "text";
    this.valueInput.value = this._value.trim();
    this.valueInput.placeholder = "value filter...";
    this.valueInput.addEventListener("input", () => this.debouncedSearch());
    this.valueInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") this.doSearch();
    });

    const closeBtn = document.createElement("button");
    closeBtn.className = "ark-tag-search-close";
    closeBtn.textContent = "\u00d7";
    closeBtn.title = "Close";
    closeBtn.addEventListener("click", () => {
      this.dispatchEvent(new CustomEvent("close", { bubbles: true }));
    });

    bar.appendChild(atSign);
    bar.appendChild(this.tagInput);
    bar.appendChild(colonSpace);
    bar.appendChild(this.regexBtn);
    bar.appendChild(this.valueInput);
    bar.appendChild(closeBtn);

    // Results area
    this.resultsEl = document.createElement("div");
    this.resultsEl.className = "ark-tag-search-results";

    // Resize handle
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

    this.appendChild(bar);
    this.appendChild(this.resultsEl);
    this.appendChild(resizeHandle);

    // Initial search
    this.resultsEl.innerHTML = '<div class="ark-tag-search-loading">Searching\u2026</div>';
    this.doSearch();
    setTimeout(() => this.valueInput.focus(), 0);
  }

  private debouncedSearch(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    this.debounceTimer = setTimeout(() => this.doSearch(), 300);
  }

  private doSearch(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    this.debounceTimer = null;

    const api = this._api;
    if (!api) return;

    const tag = this.tagInput.value.trim();
    if (!tag) {
      this.phasedResults = [];
      this.resultsEl.innerHTML = "";
      return;
    }

    // Validate tag name in literal mode
    if (!this.useRegex && !validTagName.test(tag)) {
      this.tagInput.style.borderColor = "var(--term-danger, #f87171)";
      this.tagInput.title = "Invalid tag name — must start with a letter, then letters/digits/hyphens/dots";
      return;
    }
    this.tagInput.style.borderColor = "";
    this.tagInput.title = "";

    const value = this.valueInput.value.trim();
    let query: string;
    if (this.useRegex) {
      query = value ? `@${tag}:\\s*${value}` : `@${tag}:`;
    } else {
      query = value ? `@${tag}:\\s*${escapeRegex(value)}` : `@${tag}:`;
    }

    // New search generation — stale results from prior searches are ignored
    const gen = ++this.searchGeneration;
    this.phasedResults = [];

    // Phase 1: trigram (always, immediate) R1386
    const phase1 = api.search(query, "regex").then((groups) => {
      if (gen !== this.searchGeneration) return;
      for (const g of groups) {
        this.phasedResults.push({ group: g, phase: "trigram" });
      }
      this.renderResults();
    });

    // Phase 2: embedding (if available) R1387, R1389
    const hasPhase2 = api.embedMatch && api.expandSearch;
    let phase2Matches: TagMatch[] | null = null;

    if (hasPhase2) {
      const embedQuery = value ? `${tag} ${value}` : tag;
      api.embedMatch!(embedQuery).then((matches) => {
        if (gen !== this.searchGeneration || matches.length === 0) return;
        phase2Matches = matches;
        const alts = matches.map(m => ({ tag: m.tag, value: m.value }));
        return api.expandSearch!(alts);
      }).then((groups) => {
        if (gen !== this.searchGeneration || !groups) return;
        // Deduplicate: skip paths already in phase 1 R1391
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

        // Phase 3: curation (if available) R1388
        if (api.curateRequest && api.curateResult && phase2Matches) {
          this.startCuration(api, gen, tag, value, phase2Matches);
        }
      });
    }

    // Show loading state until phase 1 resolves
    phase1.then(() => {
      if (gen !== this.searchGeneration) return;
      if (this.phasedResults.length === 0) {
        this.resultsEl.innerHTML = '<div class="ark-tag-search-empty">No results</div>';
      }
    });
  }

  /** Phase 3: queue curation and poll for result. R1388 */
  private startCuration(
    api: SearchAPI,
    gen: number,
    tag: string,
    value: string,
    candidates: TagMatch[],
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
        // Not ready yet — poll again after a short delay
        setTimeout(() => this.pollCuration(api, gen, requestId), 500);
        return;
      }
      if (result.error) return; // silently ignore curation errors

      // Build sets for quick lookup
      const curatedKeys = new Set(result.curated.map(m => `${m.tag}\0${m.value}`));
      const rejectedKeys = new Set(result.rejected.map(m => `${m.tag}\0${m.value}`));

      // Update phase for candidate results R1394
      for (const pr of this.phasedResults) {
        if (pr.phase !== "candidate") continue;
        // Match by path — candidates came from expandSearch on these tags
        // Use a simple heuristic: if any curated tag's value appears in the path's results, promote
        // For now, promote all candidates that aren't rejected
        const key = this.matchGroupToTag(pr);
        if (key && curatedKeys.has(key)) {
          pr.phase = "curated";
        } else if (key && rejectedKeys.has(key)) {
          pr.phase = "rejected";
        }
        // Candidates not in either set stay as "candidate"
      }
      this.renderResults();
    });
  }

  /** Best-effort match of a result group back to its source tag. */
  private matchGroupToTag(pr: PhasedGroup): string | null {
    // The group's strategy field often contains the tag query
    // For now, return null — all unmatched candidates stay as-is
    // This will be refined when the expandSearch response carries source tags
    void pr;
    return null;
  }

  /** Render all phased results into the results area. R1390, R1392-R1394 */
  private renderResults(): void {
    this.resultsEl.innerHTML = "";
    if (this.phasedResults.length === 0) return;

    const api = this._api!;
    for (const { group, phase } of this.phasedResults) {
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
        if (chunk.preview) {
          chunkEl.innerHTML = chunk.preview;
        } else {
          const pre = document.createElement("pre");
          pre.textContent = chunk.content.slice(0, 200);
          chunkEl.appendChild(pre);
        }
        groupEl.appendChild(chunkEl);
      }

      this.resultsEl.appendChild(groupEl);
    }
  }
}

// Register the custom element
customElements.define("ark-search", ArkSearchElement);
