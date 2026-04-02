// CRC: crc-TagCompletion.md | Seq: seq-tag-completion.md
import type {
  CompletionContext,
  CompletionResult,
} from "@codemirror/autocomplete";
import type { HostAPI } from "./host-api";

/**
 * Completion source for ark tags. Triggers on:
 * - @ at word start → tag name completion
 * - colon after @tagname: → value completion
 */
export function arkTagCompletionSource(api: HostAPI) {
  return async (
    context: CompletionContext,
  ): Promise<CompletionResult | null> => {
    // Check for @tagname: value pattern (value completion)
    const valueMatch = context.matchBefore(/@[\w][\w-]*:\s*\S*/);
    if (valueMatch) {
      const parsed = valueMatch.text.match(/^@([\w][\w-]*):\s*(.*)/);
      if (parsed) {
        const tagName = parsed[1];
        const prefix = parsed[2];
        const items = await api.tagValueComplete(tagName, prefix);
        return {
          from: valueMatch.from + valueMatch.text.indexOf(parsed[2]),
          options: items.map((item) => ({
            label: item.value,
            detail: item.count !== undefined ? `(${item.count})` : undefined,
          })),
        };
      }
    }

    // Check for @prefix (tag name completion)
    const nameMatch = context.matchBefore(/@[\w][\w-]*/);
    if (nameMatch) {
      const prefix = nameMatch.text.slice(1); // strip @
      const items = await api.tagComplete(prefix);
      return {
        from: nameMatch.from,
        options: items.map((item) => ({
          label: `@${item.name}:`,
          detail: item.description,
        })),
      };
    }

    // Check for bare @ (trigger)
    if (context.matchBefore(/@/)) {
      const items = await api.tagComplete("");
      return {
        from: context.pos - 1,
        options: items.map((item) => ({
          label: `@${item.name}:`,
          detail: item.description,
        })),
      };
    }

    return null;
  };
}
