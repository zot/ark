// CRC: crc-HostAPI.md

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
 * Contract between the viewer and its host. The host implements
 * this interface using whatever transport is available — HTTP
 * calls to ark's server, in-process Lua mcp calls, or a mock
 * for testing.
 */
export interface HostAPI {
  search(query: string, mode?: string): Promise<SearchResultGroup[]>;
  tagComplete(prefix: string): Promise<TagCompletionItem[]>;
  tagValueComplete(tag: string, prefix: string): Promise<TagValueCompletionItem[]>;
  save(path: string, content: string): Promise<void>;
  navigate(path: string): void;
  setTags(path: string, tags: Record<string, string>): Promise<void>;
  showInFolder?(path: string): Promise<void>;
}
