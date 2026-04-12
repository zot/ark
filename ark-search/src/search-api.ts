// CRC: crc-SearchAPI.md | R1352-R1355, R1384, R1385

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

/** Tag match from fuzzy or embedding search. */
export interface TagMatch {
  tag: string;
  value: string;
  count: number;
  score: number;
}

/** Tag/value pair for expansion search. */
export interface TagAlt {
  tag: string;
  value: string;
}

/** Result from curation polling. */
export interface CurateResult {
  id: string;
  curated: TagMatch[];
  rejected: TagMatch[];
  done: boolean;
  error?: string;
}

/**
 * Contract between the search component and the server.
 * The search-relevant subset of HostAPI — no CM6-specific
 * methods (save, setTags). HostAPI extends this interface.
 *
 * Optional methods enable three-phase progressive search.
 * If absent, the element falls back to trigram-only (phase 1).
 */
export interface SearchAPI {
  search(query: string, mode?: string): Promise<SearchResultGroup[]>;
  tagComplete(prefix: string): Promise<TagCompletionItem[]>;
  tagValueComplete(tag: string, prefix: string): Promise<TagValueCompletionItem[]>;
  navigate(path: string): void;
  showInFolder?(path: string): Promise<void>;

  /** Phase 2: embedding similarity search → tag matches. */
  embedMatch?(query: string, k?: number): Promise<TagMatch[]>;
  /** Phase 2: search for file results matching tag/value pairs. */
  expandSearch?(tags: TagAlt[]): Promise<SearchResultGroup[]>;
  /** Phase 3: queue Haiku curation of candidates, returns requestId. */
  curateRequest?(tag: string, value: string, candidates: TagMatch[]): Promise<string>;
  /** Phase 3: poll for curation result. */
  curateResult?(id: string): Promise<CurateResult>;

  /** Extended search with chunk-level and file-level filters. R1416-R1418 */
  searchFiltered?(query: string, request: FilteredSearchRequest): Promise<SearchResultGroup[]>;
}

/** Parameters for a chunk-level filter row. R1416 */
export interface ChunkFilterParam {
  polarity: "with" | "without";
  mode: "contains" | "fuzzy" | "regex" | "tag" | "tag-contains";
  query: string;
}

/** Full search request with filters. R1416-R1418, R1469 */
export interface FilteredSearchRequest {
  mode?: string;
  chunkFilters?: ChunkFilterParam[];
  filterFiles?: string[];
  excludeFiles?: string[];
  /** R1469: structured tag query — name tokens for T record resolution */
  nameTokens?: string[];
  /** R1469: structured tag query — value tokens for V record resolution */
  valueTokens?: string[];
  /** R1469: name match mode — "exact" or "contains" */
  nameMatch?: "exact" | "contains";
}
