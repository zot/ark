// CRC: crc-SearchAPI.md | R1352-R1355

/** A single chunk within a search result group. */
export interface SearchChunk {
  range: string;
  score: number;
  content: string;
  contentType: "markdown" | "text" | "json" | "code";
  preview: string;
}

/** A group of search results for one file. */
export interface SearchResultGroup {
  path: string;
  strategy: string;
  chunks: SearchChunk[];
}

/** Tag completion entry. */
export interface TagCompletionItem {
  name: string;
  description?: string;
}

/** Value completion entry. */
export interface TagValueCompletionItem {
  value: string;
  count?: number;
}

/**
 * Contract between the search component and the server.
 * The search-relevant subset of HostAPI — no CM6-specific
 * methods (save, setTags). HostAPI extends this interface.
 */
export interface SearchAPI {
  search(query: string, mode?: string): Promise<SearchResultGroup[]>;
  tagComplete(prefix: string): Promise<TagCompletionItem[]>;
  tagValueComplete(tag: string, prefix: string): Promise<TagValueCompletionItem[]>;
  navigate(path: string): void;
  showInFolder?(path: string): Promise<void>;
}
