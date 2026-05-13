// CRC: crc-TagWidget.md | Seq: seq-tag-click.md | R1332-R1336, R1377
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
import type { HostAPI } from "./host-api";
import { ArkSearchElement } from "../../ark-search/src/ark-search-element";
import { editMode, toggleModeEffect } from "./mode-toggle";

// Ensure ArkSearchElement is registered (side-effect import)
void ArkSearchElement;

const STATUS_VALUES = [
  "open", "accepted", "in-progress", "completed", "denied", "future",
];

// --- State management for open search panels ---

/** Toggle payload: tag location + name + initial value.
 *  tagFrom is the document offset of the ArkTag node — used as the
 *  per-instance key so two tags with the same name toggle independently. */
interface TogglePayload {
  tagFrom: number;
  tagName: string;
  tagValue: string;
}

/** Effect to toggle a search panel for a tag. */
const toggleSearchPanel = StateEffect.define<TogglePayload>();

/** Per-panel state. */
interface PanelState {
  tagName: string;
  tagValue: string;
}

/** State field tracking which tags have open search panels.
 *  Also provides block widget decorations since plugins can't do block widgets.
 *  Keyed by ArkTag node offset (tagFrom), so two @tag: instances with the same
 *  name keep separate panel state. Keys are remapped through document changes. */
function createOpenSearchPanels(api: HostAPI) {
  return StateField.define<Map<number, PanelState>>({
    create: () => new Map(),
    update(panels, tr) {
      let next = panels;
      if (tr.docChanged && panels.size > 0) {
        next = new Map();
        for (const [from, state] of panels) {
          const mapped = tr.changes.mapPos(from);
          if (mapped !== null) next.set(mapped, state);
        }
      }
      for (const effect of tr.effects) {
        if (effect.is(toggleSearchPanel)) {
          if (next === panels) next = new Map(panels);
          if (next.has(effect.value.tagFrom)) {
            next.delete(effect.value.tagFrom);
          } else {
            next.set(effect.value.tagFrom, {
              tagName: effect.value.tagName,
              tagValue: effect.value.tagValue,
            });
          }
        }
      }
      return next;
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
            const panelState = panels.get(node.from);
            if (!panelState) return;
            const line = state.doc.lineAt(node.to);
            widgets.push(
              Decoration.widget({
                widget: new TagSearchPanelWidget(node.from, panelState, api),
                block: true,
                side: 1,
              }).range(line.to),
            );
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
    private readonly tagFrom: number,
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
          tagFrom: this.tagFrom,
          tagName: this.tagName,
          tagValue: this.tagValue,
        }),
      });
    });
    return btn;
  }

  eq(other: TagSearchWidget): boolean {
    return (
      this.tagFrom === other.tagFrom &&
      this.tagText === other.tagText &&
      this.isOpen === other.isOpen
    );
  }
}

/** Block widget rendering the search panel inline below a tag line.
 *  Delegates to the `<ark-search>` custom element. R1377 */
class TagSearchPanelWidget extends WidgetType {
  constructor(
    private readonly tagFrom: number,
    private readonly state: PanelState,
    private readonly api: HostAPI,
  ) {
    super();
  }

  toDOM(view: EditorView): HTMLElement {
    const el = document.createElement("ark-search") as ArkSearchElement;
    el.api = this.api;
    el.tag = this.state.tagName;
    el.value = this.state.tagValue.trim();
    el.addEventListener("close", () => {
      view.dispatch({
        effects: toggleSearchPanel.of({
          tagFrom: this.tagFrom,
          tagName: this.state.tagName,
          tagValue: this.state.tagValue,
        }),
      });
    });
    return el;
  }

  eq(other: TagSearchPanelWidget): boolean {
    return this.tagFrom === other.tagFrom && this.state === other.state;
  }

  updateDOM(): boolean {
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
  panelsField: StateField<Map<number, PanelState>>,
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
            widget: new TagSearchWidget(node.from, tagName, tagValue, tagText, panels.has(node.from)),
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

