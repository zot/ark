// CRC: crc-ArkSearchElement.md | Seq: seq-tag-click.md | R1356-R1367, R1372, R1373, R1377

import type { SearchAPI, SearchResultGroup } from "./search-api";

/** Validate tag name: must match @tag: pattern. */
const validTagName = /^[a-zA-Z][\w.-]*$/;

/** Escape string for use in regex. */
function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * `<ark-search>` custom element — standalone tag search panel.
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

    api.search(query, "regex").then((groups) => {
      this.resultsEl.innerHTML = "";
      if (groups.length === 0) {
        this.resultsEl.innerHTML = '<div class="ark-tag-search-empty">No results</div>';
        return;
      }
      renderResults(this.resultsEl, groups, api);
    });
  }
}

/** Render search results with path links and show-in-folder buttons. */
function renderResults(
  container: HTMLElement,
  groups: SearchResultGroup[],
  api: SearchAPI,
): void {
  for (const group of groups) {
    const groupEl = document.createElement("div");
    groupEl.className = "ark-tag-search-group";

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

    container.appendChild(groupEl);
  }
}

// Register the custom element
customElements.define("ark-search", ArkSearchElement);
