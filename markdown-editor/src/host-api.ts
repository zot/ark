// CRC: crc-HostAPI.md | R1326-R1328, R1353

export type {
  SearchAPI,
  SearchChunk,
  SearchResultGroup,
  TagCompletionItem,
  TagValueCompletionItem,
} from "../../ark-search/src/search-api";

import type { SearchAPI } from "../../ark-search/src/search-api";

/**
 * Contract between the viewer and its host. Extends SearchAPI
 * with CM6-specific methods. The host implements this interface
 * using whatever transport is available — HTTP calls to ark's
 * server, in-process Lua mcp calls, or a mock for testing.
 */
export interface HostAPI extends SearchAPI {
  save(path: string, content: string): Promise<void>;
  setTags(path: string, tags: Record<string, string>): Promise<void>;
}
