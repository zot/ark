// CRC: crc-ModeToggle.md | Seq: seq-mode-toggle.md, seq-save.md
import { StateField, StateEffect } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import type { HostAPI } from "./host-api";

/** Effect to toggle read/edit mode. */
export const toggleModeEffect = StateEffect.define<boolean>();

/** State field tracking whether the editor is in edit mode. */
export const editMode = StateField.define<boolean>({
  create: () => false,
  update(value, tr) {
    for (const effect of tr.effects) {
      if (effect.is(toggleModeEffect)) return effect.value;
    }
    return value;
  },
});

/** Toggle between read and edit mode. */
export function toggleMode(view: EditorView): boolean {
  const current = view.state.field(editMode);
  view.dispatch({
    effects: toggleModeEffect.of(!current),
  });
  return true;
}

/** Save the current document content. */
export function saveDocument(
  view: EditorView,
  api: HostAPI,
  path: string,
): void {
  const content = view.state.doc.toString();
  api.save(path, content);
}

/** Extension that makes the editor read-only by default and provides mode toggle. */
export function modeToggleExtension(api: HostAPI, path: string) {
  return [
    editMode,
    // Editable facet computed from editMode StateField — single source of truth
    EditorView.editable.compute([editMode], (state) => state.field(editMode)),
    keymap.of([
      {
        key: "Mod-e",
        run: toggleMode,
      },
      {
        key: "Mod-s",
        run(view) {
          saveDocument(view, api, path);
          return true;
        },
      },
    ]),
  ];
}
