// Spike: ink-mde with ark extensions
// Test whether our CM6 extensions compose with ink-mde's editor
import ink, { definePlugin, pluginTypes } from "ink-mde";
import type { HostAPI } from "./host-api";
import { arkTagExtension } from "./ark-tag-parser";
import { tagWidgetExtension } from "./tag-widget";
import { arkTagCompletionSource } from "./tag-completion";
import { arkSearchBlockExtension } from "./ark-search-block";

export type { HostAPI, SearchResultGroup, SearchChunk } from "./host-api";

export interface InkArkConfig {
  parent: HTMLElement;
  doc: string;
  path: string;
  api: HostAPI;
}

/** Create an ink-mde editor with ark extensions injected. */
export function createInkArkEditor(config: InkArkConfig) {
  const { parent, doc, path, api } = config;

  return ink(parent, {
    doc,
    interface: {
      appearance: "auto",
      autocomplete: true,
      images: true,
      lists: true,
      spellcheck: true,
      toolbar: false,
    },
    plugins: [
      // Inject ark tag parser into markdown grammar
      definePlugin({
        type: pluginTypes.grammar,
        value: arkTagExtension,
      }),
      // Inject tag widgets as a CM6 extension
      definePlugin({
        type: pluginTypes.default,
        value: tagWidgetExtension(api, path),
      }),
      // Inject ark-search block rendering
      definePlugin({
        type: pluginTypes.default,
        value: arkSearchBlockExtension(api),
      }),
      // Inject tag completion
      definePlugin({
        type: pluginTypes.completion,
        value: arkTagCompletionSource(api),
      }),
    ],
  });
}
