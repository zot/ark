// CRC: crc-TagWidget.md | Seq: seq-tag-click.md
// R1200-R1215, R1222-R1224
import {
  EditorView,
  Decoration,
  DecorationSet,
  ViewPlugin,
  ViewUpdate,
  WidgetType,
} from "@codemirror/view";
import { StateEffect, StateField } from "@codemirror/state";
import { syntaxTree } from "@codemirror/language";
import { Range } from "@codemirror/state";
import type { HostAPI, SearchResultGroup } from "./host-api";
import { editMode, toggleModeEffect } from "./mode-toggle";

const STATUS_VALUES = [
  "open", "accepted", "in-progress", "completed", "denied", "future",
];

// --- State management for open search panels ---

/** Toggle payload: tag name + initial value. */
interface TogglePayload {
  tagName: string;
  tagValue: string;
}

/** Effect to toggle a search panel for a tag. */
const toggleSearchPanel = StateEffect.define<TogglePayload>();

/** Per-panel state. */
interface PanelState {
  tagName: string;
  tagValue: string;
  results: SearchResultGroup[] | null;
  loading: boolean;
}

/** State field tracking which tags have open search panels.
 *  Also provides block widget decorations since plugins can't do block widgets. */
function createOpenSearchPanels(api: HostAPI) {
  return StateField.define<Map<string, PanelState>>({
    create: () => new Map(),
    update(panels, tr) {
      for (const effect of tr.effects) {
        if (effect.is(toggleSearchPanel)) {
          const next = new Map(panels);
          if (next.has(effect.value.tagName)) {
            next.delete(effect.value.tagName);
          } else {
            next.set(effect.value.tagName, {
              tagName: effect.value.tagName,
              tagValue: effect.value.tagValue,
              results: null,
              loading: true,
            });
          }
          return next;
        }
      }
      return panels;
    },
    provide: (field) =>
      EditorView.decorations.compute([field], (state) => {
        const panels = state.field(field);
        if (panels.size === 0) return Decoration.none;
        const widgets: Range<Decoration>[] = [];
        const tree = syntaxTree(state);

        tree.iterate({
          enter(node) {
            if (node.name !== "ArkTag") return;
            const nameNode = node.node.getChild("ArkTagName");
            if (!nameNode) return;
            const tagName = state.sliceDoc(nameNode.from, nameNode.to);
            const panelState = panels.get(tagName);
            if (panelState) {
              const line = state.doc.lineAt(node.to);
              widgets.push(
                Decoration.widget({
                  widget: new TagSearchPanelWidget(panelState, api),
                  block: true,
                  side: 1,
                }).range(line.to),
              );
            }
          },
        });

        return Decoration.set(widgets, true);
      }),
  });
}

// --- Widgets ---

/** Widget shown after a tag — click to toggle search panel. */
class TagSearchWidget extends WidgetType {
  constructor(
    private readonly tagName: string,
    private readonly tagValue: string,
    private readonly tagText: string,
    private readonly isOpen: boolean = false,
  ) {
    super();
  }

  toDOM(view: EditorView): HTMLElement {
    const btn = document.createElement("span");
    btn.className = "ark-tag-action";
    btn.textContent = this.isOpen ? " \u25BC" : " \u25B6";
    btn.title = this.isOpen ? "Close search" : `Search: ${this.tagText}`;
    btn.style.cursor = "pointer";
    btn.style.opacity = "0.5";
    btn.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
      view.dispatch({
        effects: toggleSearchPanel.of({
          tagName: this.tagName,
          tagValue: this.tagValue,
        }),
      });
    });
    return btn;
  }

  eq(other: TagSearchWidget): boolean {
    return this.tagText === other.tagText && this.isOpen === other.isOpen;
  }
}

/** Block widget rendering the search panel inline below a tag line. */
class TagSearchPanelWidget extends WidgetType {
  constructor(
    private readonly state: PanelState,
    private readonly api: HostAPI,
  ) {
    super();
  }

  toDOM(view: EditorView): HTMLElement {
    const panel = document.createElement("div");
    panel.className = "ark-tag-search-panel";
    // Prevent CM from intercepting events inside the panel
    for (const evt of ["mousedown", "keydown", "keyup", "keypress", "focus", "input"] as const) {
      panel.addEventListener(evt, (e) => e.stopPropagation());
    }

    // Query bar
    const bar = document.createElement("div");
    bar.className = "ark-tag-search-bar";

    const atSign = document.createElement("span");
    atSign.className = "ark-tag-search-at";
    atSign.textContent = "@";

    const tagInput = document.createElement("input");
    tagInput.className = "ark-tag-search-tag";
    tagInput.type = "text";
    tagInput.value = this.state.tagName;
    tagInput.placeholder = "tag";
    tagInput.size = Math.max(this.state.tagName.length + 2, 8);
    tagInput.addEventListener("input", () => {
      tagInput.size = Math.max(tagInput.value.length + 2, 8);
    });

    const colonSpace = document.createElement("span");
    colonSpace.className = "ark-tag-search-colon";
    colonSpace.textContent = ": ";

    let useRegex = false;
    const regexBtn = document.createElement("button");
    regexBtn.className = "ark-tag-search-regex";
    regexBtn.textContent = "Aa";
    regexBtn.title = "Toggle regex mode";
    regexBtn.addEventListener("click", () => {
      useRegex = !useRegex;
      regexBtn.textContent = useRegex ? ".*" : "Aa";
      regexBtn.classList.toggle("active", useRegex);
      doSearch();
    });

    const valueInput = document.createElement("input");
    valueInput.className = "ark-tag-search-value";
    valueInput.type = "text";
    valueInput.value = this.state.tagValue.trim();
    valueInput.placeholder = "value filter...";

    const closeBtn = document.createElement("button");
    closeBtn.className = "ark-tag-search-close";
    closeBtn.textContent = "\u00d7";
    closeBtn.title = "Close";
    closeBtn.addEventListener("click", () => {
      view.dispatch({ effects: toggleSearchPanel.of({ tagName: this.state.tagName, tagValue: this.state.tagValue }) });
    });

    bar.appendChild(atSign);
    bar.appendChild(tagInput);
    bar.appendChild(colonSpace);
    bar.appendChild(regexBtn);
    bar.appendChild(valueInput);
    bar.appendChild(closeBtn);

    // Results area
    const results = document.createElement("div");
    results.className = "ark-tag-search-results";

    // Resize handle
    const resizeHandle = document.createElement("div");
    resizeHandle.className = "ark-tag-search-resize";
    let startY = 0, startH = 0;
    resizeHandle.addEventListener("mousedown", (e) => {
      startY = e.clientY;
      startH = results.offsetHeight;
      const onMove = (ev: MouseEvent) => {
        results.style.height = Math.max(100, startH + ev.clientY - startY) + "px";
      };
      const onUp = () => {
        document.removeEventListener("mousemove", onMove);
        document.removeEventListener("mouseup", onUp);
      };
      document.addEventListener("mousemove", onMove);
      document.addEventListener("mouseup", onUp);
    });

    panel.appendChild(bar);
    panel.appendChild(results);
    panel.appendChild(resizeHandle);

    // Search logic
    const api = this.api;
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;

    // Validate tag name: must match @tag: pattern (letters, digits, hyphens, dots)
    const validTagName = /^[a-zA-Z][\w.-]*$/;

    // Escape string for use in regex
    const escapeRegex = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

    const doSearch = () => {
      const tag = tagInput.value.trim();
      if (!tag) {
        results.innerHTML = "";
        return;
      }

      // Validate tag name (regex mode allows anything)
      if (!useRegex && !validTagName.test(tag)) {
        tagInput.style.borderColor = "var(--term-danger, #f87171)";
        tagInput.title = "Invalid tag name — must start with a letter, then letters/digits/hyphens/dots";
        return;
      }
      tagInput.style.borderColor = "";
      tagInput.title = "";

      const value = valueInput.value.trim();
      let query: string;
      if (useRegex) {
        // Regex mode: tag name and value are raw regex
        query = value ? `@${tag}:\\s*${value}` : `@${tag}:`;
      } else {
        // Literal mode: escape the value for safe regex matching
        query = value ? `@${tag}:\\s*${escapeRegex(value)}` : `@${tag}:`;
      }

      // Keep existing results visible while loading — avoids flicker
      // from height changes during CM block widget relayout
      api.search(query, "regex").then((groups) => {
        results.innerHTML = "";
        if (groups.length === 0) {
          results.innerHTML = '<div class="ark-tag-search-empty">No results</div>';
          return;
        }
        renderTagSearchResults(results, groups, api);
      });
    };

    const debouncedSearch = () => {
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(doSearch, 300);
    };

    valueInput.addEventListener("input", debouncedSearch);
    valueInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        if (debounceTimer) clearTimeout(debounceTimer);
        doSearch();
      }
    });
    tagInput.addEventListener("input", debouncedSearch);
    tagInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        if (debounceTimer) clearTimeout(debounceTimer);
        doSearch();
      }
    });

    // Initial search — show loading state first
    results.innerHTML = '<div class="ark-tag-search-loading">Searching\u2026</div>';
    doSearch();
    setTimeout(() => valueInput.focus(), 0);

    return panel;
  }

  eq(other: TagSearchPanelWidget): boolean {
    // Same tag = same widget — CM preserves the DOM
    return this.state === other.state;
  }

  updateDOM(): boolean {
    // Never replace the DOM — the panel manages its own state
    return true;
  }

  get estimatedHeight(): number {
    return 200;
  }
}

/** Widget for status tags — dropdown with known values. */
class StatusWidget extends WidgetType {
  constructor(
    private readonly currentValue: string,
    private readonly path: string,
    private readonly api: HostAPI,
  ) {
    super();
  }

  toDOM(): HTMLElement {
    const select = document.createElement("select");
    select.className = "ark-status-dropdown";
    for (const v of STATUS_VALUES) {
      const opt = document.createElement("option");
      opt.value = v;
      opt.textContent = v;
      opt.selected = v === this.currentValue.trim();
      select.appendChild(opt);
    }
    select.addEventListener("change", () => {
      this.api.setTags(this.path, { status: select.value });
    });
    return select;
  }

  eq(other: StatusWidget): boolean {
    return this.currentValue === other.currentValue;
  }
}

// --- Decoration builder ---

/** Check whether a ViewUpdate requires decoration rebuild. */
export function needsRedecoration(update: ViewUpdate): boolean {
  return (
    update.docChanged ||
    update.viewportChanged ||
    update.transactions.some((tr) =>
      tr.effects.some((e) => e.is(toggleModeEffect) || e.is(toggleSearchPanel)),
    )
  );
}

/** Build inline decorations for all ArkTag nodes in the visible ranges.
 *  Block widgets (search panels) are provided by the StateField. */
function buildTagDecorations(
  view: EditorView,
  api: HostAPI,
  path: string,
  panelsField: StateField<Map<string, PanelState>>,
): DecorationSet {
  const isEditing = view.state.field(editMode, false);
  if (isEditing) return Decoration.none;

  const panels = view.state.field(panelsField);

  const widgets: Range<Decoration>[] = [];
  const tree = syntaxTree(view.state);

  for (const { from, to } of view.visibleRanges) {
    tree.iterate({
      from,
      to,
      enter(node) {
        if (node.name !== "ArkTag") return;

        const nameNode = node.node.getChild("ArkTagName");
        if (!nameNode) return;
        const tagName = view.state.sliceDoc(nameNode.from, nameNode.to);

        const valueNode = node.node.getChild("ArkTagValue");
        const tagValue = valueNode
          ? view.state.sliceDoc(valueNode.from, valueNode.to)
          : "";

        const tagText = view.state.sliceDoc(node.from, node.to);

        // Status tags: replace the value text with a dropdown
        if (tagName === "status" && valueNode) {
          widgets.push(
            Decoration.replace({
              widget: new StatusWidget(tagValue, path, api),
            }).range(valueNode.from, valueNode.to),
          );
        }

        // All tags get the search button — placed before the tag for stable position
        widgets.push(
          Decoration.widget({
            widget: new TagSearchWidget(tagName, tagValue, tagText, panels.has(tagName)),
            side: -1,
          }).range(node.from),
        );
      },
    });
  }

  return Decoration.set(widgets, true);
}

/** ViewPlugin that manages tag widget decorations. */
export function tagWidgetExtension(api: HostAPI, path: string) {
  const panelsField = createOpenSearchPanels(api);
  return [
    panelsField,
    ViewPlugin.fromClass(
      class {
        decorations: DecorationSet;
        constructor(view: EditorView) {
          this.decorations = buildTagDecorations(view, api, path, panelsField);
        }
        update(update: ViewUpdate) {
          if (needsRedecoration(update)) {
            this.decorations = buildTagDecorations(update.view, api, path, panelsField);
          }
        }
      },
      { decorations: (v) => v.decorations },
    ),
  ];
}

// --- Result rendering ---

/** Render search results with show-location buttons. R1211-R1214 */
function renderTagSearchResults(
  container: HTMLElement,
  groups: SearchResultGroup[],
  api: HostAPI,
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
