// CRC: crc-TagWidget.md | Seq: seq-tag-click.md
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
import { editMode, toggleModeEffect } from "./mode-toggle";

const STATUS_VALUES = [
  "open", "accepted", "in-progress", "completed", "denied", "future",
];

/** Widget shown after a tag — click to search. */
class TagSearchWidget extends WidgetType {
  constructor(
    private readonly tagText: string,
    private readonly api: HostAPI,
  ) {
    super();
  }

  toDOM(): HTMLElement {
    const btn = document.createElement("span");
    btn.className = "ark-tag-action";
    btn.textContent = " \u25B6";
    btn.title = `Search: ${this.tagText}`;
    btn.style.cursor = "pointer";
    btn.style.opacity = "0.5";
    btn.addEventListener("click", (e) => {
      e.preventDefault();
      // TODO: open search panel below the line with tagText pre-selected
      // For now, fires search but doesn't display results
      this.api.search(this.tagText);
    });
    return btn;
  }

  eq(other: TagSearchWidget): boolean {
    return this.tagText === other.tagText;
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

/** Check whether a ViewUpdate requires decoration rebuild. */
export function needsRedecoration(update: ViewUpdate): boolean {
  return (
    update.docChanged ||
    update.viewportChanged ||
    update.transactions.some((tr) =>
      tr.effects.some((e) => e.is(toggleModeEffect)),
    )
  );
}

/** Build decorations for all ArkTag nodes in the visible ranges. */
function buildTagDecorations(
  view: EditorView,
  api: HostAPI,
  path: string,
): DecorationSet {
  const isEditing = view.state.field(editMode, false);
  if (isEditing) return Decoration.none;

  const widgets: Range<Decoration>[] = [];
  const tree = syntaxTree(view.state);

  for (const { from, to } of view.visibleRanges) {
    tree.iterate({
      from,
      to,
      enter(node) {
        if (node.name !== "ArkTag") return;

        // Extract tag name and value from parse tree children
        const nameNode = node.node.getChild("ArkTagName");
        if (!nameNode) return;
        const tagName = view.state.sliceDoc(nameNode.from, nameNode.to);

        const valueNode = node.node.getChild("ArkTagValue");
        const tagValue = valueNode
          ? view.state.sliceDoc(valueNode.from, valueNode.to)
          : "";

        const tagText = view.state.sliceDoc(node.from, node.to);

        let widget: WidgetType;
        if (tagName === "status") {
          widget = new StatusWidget(tagValue, path, api);
        } else {
          widget = new TagSearchWidget(tagText, api);
        }

        widgets.push(
          Decoration.widget({ widget, side: 1 }).range(node.to),
        );
      },
    });
  }

  return Decoration.set(widgets, true);
}

/** ViewPlugin that manages tag widget decorations. */
export function tagWidgetExtension(api: HostAPI, path: string) {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = buildTagDecorations(view, api, path);
      }
      update(update: ViewUpdate) {
        if (needsRedecoration(update)) {
          this.decorations = buildTagDecorations(update.view, api, path);
        }
      }
    },
    { decorations: (v) => v.decorations },
  );
}
