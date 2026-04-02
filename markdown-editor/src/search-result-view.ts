// CRC: crc-SearchResultView.md | Seq: seq-tag-click.md, seq-ark-search-render.md
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { markdown } from "@codemirror/lang-markdown";
import type { HostAPI, SearchResultGroup, SearchChunk } from "./host-api";
import { arkTagExtension } from "./ark-tag-parser";
import { tagWidgetExtension } from "./tag-widget";

/** Track EditorView instances for cleanup. */
const managedViews = new WeakMap<HTMLElement, EditorView[]>();

/** Destroy any EditorViews previously created inside a container. */
function cleanupViews(container: HTMLElement): void {
  const views = managedViews.get(container);
  if (views) {
    for (const view of views) view.destroy();
    managedViews.delete(container);
  }
}

/** Track an EditorView created inside a container. */
function trackView(container: HTMLElement, view: EditorView): void {
  let views = managedViews.get(container);
  if (!views) {
    views = [];
    managedViews.set(container, views);
  }
  views.push(view);
}

/**
 * Create a read-only CM6 state with ark extensions.
 * Shared between main editor (for results) and search-result-view.
 */
function createReadOnlyArkState(doc: string, api: HostAPI, path: string): EditorState {
  return EditorState.create({
    doc,
    extensions: [
      basicSetup,
      markdown({ extensions: [arkTagExtension] }),
      tagWidgetExtension(api, path),
      EditorView.editable.of(false),
    ],
  });
}

/**
 * Render a list of search result groups into a container element.
 * Markdown chunks get read-only CM6 instances with tag widgets;
 * non-markdown chunks use pre-rendered HTML.
 */
export function renderSearchResults(
  container: HTMLElement,
  results: SearchResultGroup[],
  api: HostAPI,
): void {
  cleanupViews(container);
  container.innerHTML = "";

  for (const group of results) {
    const groupEl = document.createElement("div");
    groupEl.className = "ark-search-result-group";

    const header = document.createElement("div");
    header.className = "ark-search-result-path";
    header.textContent = group.path;
    header.style.cursor = "pointer";
    header.addEventListener("click", () => api.navigate(group.path));
    groupEl.appendChild(header);

    for (const chunk of group.chunks) {
      const chunkEl = document.createElement("div");
      chunkEl.className = "ark-search-result-chunk";
      renderChunk(chunkEl, container, chunk, group.path, api);
      groupEl.appendChild(chunkEl);
    }

    container.appendChild(groupEl);
  }
}

/** Render a single chunk — CM6 for markdown, HTML for everything else. */
function renderChunk(
  chunkEl: HTMLElement,
  container: HTMLElement,
  chunk: SearchChunk,
  path: string,
  api: HostAPI,
): void {
  if (chunk.contentType === "markdown") {
    const view = new EditorView({
      parent: chunkEl,
      state: createReadOnlyArkState(chunk.content, api, path),
    });
    trackView(container, view);
  } else {
    const div = document.createElement("div");
    div.className = "ark-search-result-preview";
    div.innerHTML = chunk.preview;
    chunkEl.appendChild(div);
  }
}
