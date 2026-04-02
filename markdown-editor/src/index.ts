// Ark Markdown Editor — CM6 component with interactive tag support
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { markdown } from "@codemirror/lang-markdown";
import { autocompletion } from "@codemirror/autocomplete";
import type { HostAPI } from "./host-api";
import { arkTagExtension } from "./ark-tag-parser";
import { tagWidgetExtension } from "./tag-widget";
import { arkTagCompletionSource } from "./tag-completion";
import { arkSearchBlockExtension } from "./ark-search-block";
import { modeToggleExtension } from "./mode-toggle";

export type { HostAPI, SearchResultGroup, SearchChunk } from "./host-api";
export { toggleMode, editMode } from "./mode-toggle";

export interface ArkEditorConfig {
  parent: HTMLElement;
  doc: string;
  path: string;
  api: HostAPI;
}

/** Create a fully configured ark markdown editor. */
export function createArkEditor(config: ArkEditorConfig): EditorView {
  const { parent, doc, path, api } = config;

  return new EditorView({
    parent,
    state: EditorState.create({
      doc,
      extensions: [
        basicSetup,
        markdown({ extensions: [arkTagExtension] }),
        tagWidgetExtension(api, path),
        arkSearchBlockExtension(api),
        autocompletion({ override: [arkTagCompletionSource(api)] }),
        modeToggleExtension(api, path),
      ],
    }),
  });
}
