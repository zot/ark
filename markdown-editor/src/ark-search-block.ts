// CRC: crc-ArkSearchBlock.md | Seq: seq-ark-search-render.md, seq-mode-toggle.md
import {
  EditorView,
  Decoration,
  DecorationSet,
  ViewPlugin,
  ViewUpdate,
  WidgetType,
} from "@codemirror/view";
import { syntaxTree } from "@codemirror/language";
import { Range } from "@codemirror/state";
import type { HostAPI, SearchResultGroup } from "./host-api";
import { editMode } from "./mode-toggle";
import { needsRedecoration } from "./tag-widget";
import { renderSearchResults } from "./search-result-view";

type ViewMode = "both" | "results" | "src";

const DEFAULT_MODES: ViewMode[] = ["both", "results", "src"];
const RESULT_DEFAULT_MODES: ViewMode[] = ["src", "both", "results"];

/** Parse mode= attribute from fence info string. */
function parseModes(info: string): ViewMode[] {
  const match = info.match(/mode=([\w,]+)/);
  if (!match) return DEFAULT_MODES;
  const modes = match[1].split(",").filter(
    (m): m is ViewMode => m === "both" || m === "results" || m === "src",
  );
  return modes.length > 0 ? modes : DEFAULT_MODES;
}

/** State for a single ark-search block. */
interface BlockState {
  query: string;
  modes: ViewMode[];
  currentMode: ViewMode;
  results: SearchResultGroup[] | null;
  loading: boolean;
}

/** Widget that replaces an ark-search code block. */
class ArkSearchWidget extends WidgetType {
  private container: HTMLElement | null = null;

  constructor(
    private state: BlockState,
    private readonly api: HostAPI,
    private readonly onModeChange: (mode: ViewMode) => void,
  ) {
    super();
  }

  toDOM(): HTMLElement {
    this.container = document.createElement("div");
    this.container.className = "ark-search-block";
    this.render();
    return this.container;
  }

  private render(): void {
    if (!this.container) return;
    this.container.innerHTML = "";

    if (this.state.modes.length > 1) {
      const toggle = document.createElement("button");
      toggle.className = "ark-search-mode-toggle";
      toggle.textContent = this.state.currentMode;
      toggle.title = `Modes: ${this.state.modes.join(" \u2192 ")}`;
      toggle.addEventListener("click", () => {
        const idx = this.state.modes.indexOf(this.state.currentMode);
        const next = this.state.modes[(idx + 1) % this.state.modes.length];
        this.onModeChange(next);
      });
      this.container.appendChild(toggle);
    }

    const mode = this.state.currentMode;

    if (mode === "both" || mode === "src") {
      const src = document.createElement("pre");
      src.className = "ark-search-source";
      const code = document.createElement("code");
      code.textContent = this.state.query;
      src.appendChild(code);
      this.container.appendChild(src);
    }

    if (mode === "both" || mode === "results") {
      const resultsEl = document.createElement("div");
      resultsEl.className = "ark-search-results";

      if (this.state.loading) {
        resultsEl.textContent = "Searching\u2026";
      } else if (this.state.results) {
        renderSearchResults(resultsEl, this.state.results, this.api);
      }
      this.container.appendChild(resultsEl);
    }
  }

  eq(other: ArkSearchWidget): boolean {
    return (
      this.state.query === other.state.query &&
      this.state.currentMode === other.state.currentMode &&
      this.state.loading === other.state.loading &&
      this.state.results === other.state.results
    );
  }
}

/**
 * Extension for ark-search fenced code blocks.
 * Set inResults=true when embedding inside search results
 * (defaults to src-first mode order).
 */
export function arkSearchBlockExtension(
  api: HostAPI,
  inResults: boolean = false,
) {
  // Key by query content for stability across edits
  const blockStates = new Map<string, BlockState>();

  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      view: EditorView;

      constructor(view: EditorView) {
        this.view = view;
        this.decorations = this.build(view);
      }

      executeSearch(key: string, query: string) {
        const state = blockStates.get(key);
        if (!state || state.loading) return;
        state.loading = true;
        // Force redraw to show loading state
        this.view.dispatch({});
        api.search(query).then((results) => {
          state.results = results;
          state.loading = false;
          // Dispatch to trigger redecoration with results
          this.view.dispatch({});
        });
      }

      build(view: EditorView): DecorationSet {
        const isEditing = view.state.field(editMode);
        const widgets: Range<Decoration>[] = [];
        const tree = syntaxTree(view.state);
        const seenKeys = new Set<string>();

        tree.iterate({
          enter: (node) => {
            if (node.name !== "FencedCode") return;

            const firstLine = view.state.doc.lineAt(node.from);
            const info = firstLine.text;
            if (!info.match(/^```+\s*ark-search/)) return;

            const lastLine = view.state.doc.lineAt(node.to);
            const startLine = firstLine.number + 1;
            const endLine = lastLine.number - (lastLine.text.match(/^```/) ? 1 : 0);
            let query = "";
            for (let i = startLine; i <= endLine; i++) {
              const line = view.state.doc.line(i);
              query += (query ? "\n" : "") + line.text;
            }

            // Key by query content — stable across position shifts
            const key = query;
            seenKeys.add(key);

            const modes = isEditing
              ? DEFAULT_MODES
              : parseModes(info);
            const defaultModes = inResults ? RESULT_DEFAULT_MODES : modes;

            if (!blockStates.has(key)) {
              blockStates.set(key, {
                query,
                modes: defaultModes,
                currentMode: defaultModes[0],
                results: null,
                loading: false,
              });
              if (defaultModes[0] !== "src") {
                this.executeSearch(key, query);
              }
            }

            const state = blockStates.get(key)!;
            state.modes = isEditing ? DEFAULT_MODES : defaultModes;

            const widget = new ArkSearchWidget(state, api, (mode) => {
              state.currentMode = mode;
              if ((mode === "both" || mode === "results") && !state.results) {
                this.executeSearch(key, state.query);
              }
              view.dispatch({});
            });

            widgets.push(
              Decoration.replace({ widget }).range(node.from, node.to),
            );
          },
        });

        // Prune stale entries
        for (const key of blockStates.keys()) {
          if (!seenKeys.has(key)) blockStates.delete(key);
        }

        return Decoration.set(widgets, true);
      }

      update(update: ViewUpdate) {
        if (needsRedecoration(update)) {
          this.decorations = this.build(update.view);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );
}
