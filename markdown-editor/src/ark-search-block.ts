// CRC: crc-ArkSearchBlock.md | Seq: seq-ark-search-render.md, seq-mode-toggle.md | R1339-R1343, R1347
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
import type { HostAPI } from "./host-api";
import { ArkSearchElement } from "../../ark-search/src/ark-search-element";
import { editMode } from "./mode-toggle";
import { needsRedecoration } from "./tag-widget";

// Ensure ArkSearchElement is registered
void ArkSearchElement;

type ViewMode = "both" | "results" | "src";

const DEFAULT_MODES: ViewMode[] = ["both", "results", "src"];
const RESULT_DEFAULT_MODES: ViewMode[] = ["src", "both", "results"];

/** CSS to hide original fenced code block lines when widget is active. */
const hideLineStyle = EditorView.baseTheme({
  ".ark-search-hidden-line": { display: "none" },
});

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
}

/** Widget that renders an ark-search code block.
 *  Delegates result rendering to the `<ark-search>` element. */
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
      // Delegate to <ark-search> element with hidden bar
      const el = document.createElement("ark-search") as ArkSearchElement;
      el.hideBar = true;
      el.query = this.state.query;
      el.api = this.api;
      this.container.appendChild(el);
    }
  }

  eq(other: ArkSearchWidget): boolean {
    return (
      this.state.query === other.state.query &&
      this.state.currentMode === other.state.currentMode
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

  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;

      constructor(view: EditorView) {
        this.decorations = this.build(view);
      }

      build(view: EditorView): DecorationSet {
        const isEditing = view.state.field(editMode, false);
        const decos: Range<Decoration>[] = [];
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
              });
            }

            const state = blockStates.get(key)!;
            state.modes = isEditing ? DEFAULT_MODES : defaultModes;

            if (!isEditing) {
              // Hide each line of the original fenced code block
              for (let ln = firstLine.number; ln <= lastLine.number; ln++) {
                const line = view.state.doc.line(ln);
                decos.push(
                  Decoration.line({ class: "ark-search-hidden-line" }).range(line.from),
                );
              }

              // Place the widget after the last line of the block
              const widget = new ArkSearchWidget(state, api, (mode) => {
                state.currentMode = mode;
                view.dispatch({});
              });

              decos.push(
                Decoration.widget({ widget, side: 1 }).range(lastLine.to),
              );
            }
          },
        });

        // Prune stale entries
        for (const key of blockStates.keys()) {
          if (!seenKeys.has(key)) blockStates.delete(key);
        }

        return Decoration.set(decos, true);
      }

      update(update: ViewUpdate) {
        if (needsRedecoration(update)) {
          this.decorations = this.build(update.view);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );

  return [plugin, hideLineStyle];
}
