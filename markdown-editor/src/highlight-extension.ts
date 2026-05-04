// CRC: crc-HighlightExtension.md | Seq: seq-content-fetching.md | R1454-R1466
// Decoration-mark extension that highlights regex matches inside the
// visible viewport. Fed by the `highlight=<regex>` URL params that
// <ark-search> appends to iframe preview URLs, and updated at runtime
// via `ark-set-highlights` postMessage from the parent window so
// the iframe can swap highlight patterns without reloading.

import {
  EditorView,
  Decoration,
  ViewPlugin,
  ViewUpdate,
  DecorationSet,
} from "@codemirror/view";
import { Range, StateEffect, StateField } from "@codemirror/state";

const highlightMark = Decoration.mark({ class: "ark-search-highlight" });

/** Compile a list of regex strings into RegExp objects. Bad patterns
 *  are silently dropped — the search element is responsible for
 *  building well-formed regexes. */
function compile(patterns: string[]): RegExp[] {
  const out: RegExp[] = [];
  for (const p of patterns) {
    try {
      out.push(new RegExp(p, "gm"));
    } catch {
      // skip
    }
  }
  return out;
}

/** Effect fired by the parent window (via postMessage) to swap
 *  highlight patterns on an existing editor. R1466 */
const setHighlightPatternsEffect = StateEffect.define<string[]>();

export function highlightExtension(initialPatterns: string[]) {
  // State field holding the compiled regex list. Updated by the effect.
  const patternsField = StateField.define<RegExp[]>({
    create: () => compile(initialPatterns),
    update(patterns, tr) {
      for (const e of tr.effects) {
        if (e.is(setHighlightPatternsEffect)) {
          return compile(e.value);
        }
      }
      return patterns;
    },
  });

  let scrolledOnce = false;

  const plugin = ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      messageListener: ((e: MessageEvent) => void) | null = null;

      constructor(view: EditorView) {
        this.decorations = this.build(view);

        // Listen for live highlight updates from the parent window.
        // The parent (<ark-search>) posts this message when the user
        // edits the query without changing the result set, so we can
        // re-highlight in place instead of reloading the iframe. R1466
        this.messageListener = (e: MessageEvent) => {
          if (
            e.data &&
            e.data.type === "ark-set-highlights" &&
            Array.isArray(e.data.patterns)
          ) {
            view.dispatch({
              effects: setHighlightPatternsEffect.of(e.data.patterns),
            });
          }
        };
        window.addEventListener("message", this.messageListener);

        // Auto-scroll to the first match on first render. R1462
        if (!scrolledOnce) {
          const first = this.firstMatch(view);
          if (first !== -1) {
            scrolledOnce = true;
            queueMicrotask(() => {
              view.dispatch({
                effects: EditorView.scrollIntoView(first, { y: "center" }),
              });
            });
          }
        }
      }

      destroy() {
        if (this.messageListener) {
          window.removeEventListener("message", this.messageListener);
          this.messageListener = null;
        }
      }

      update(update: ViewUpdate) {
        const patternsChanged =
          update.startState.field(patternsField, false) !==
          update.state.field(patternsField, false);
        if (update.docChanged || update.viewportChanged || patternsChanged) {
          this.decorations = this.build(update.view);
        }
      }

      // CRC: crc-HighlightExtension.md | R2132
      build(view: EditorView): DecorationSet {
        const regexes = view.state.field(patternsField, false) ?? [];
        if (regexes.length === 0) return Decoration.none;
        const taken: Array<{ start: number; end: number }> = [];
        const overlaps = (s: number, e: number) =>
          taken.some((t) => s < t.end && t.start < e);
        for (const { from, to } of view.visibleRanges) {
          const text = view.state.sliceDoc(from, to);
          for (const re of regexes) {
            re.lastIndex = 0;
            let m: RegExpExecArray | null;
            while ((m = re.exec(text)) !== null) {
              if (m[0].length === 0) {
                re.lastIndex++;
                continue;
              }
              // If a capture group exists, highlight only the group;
              // otherwise highlight the full match. The group sits at
              // the end of the match — `@NAME:[^\n]*?(TOKEN)` — so
              // offset = full length - group length.
              const hasGroup = m[1] !== undefined;
              const gText = hasGroup ? m[1] : m[0];
              const gLen = gText.length;
              const gOffset = hasGroup ? m[0].length - gLen : 0;
              let start = from + m.index + gOffset;
              let end = start + gLen;
              // If this range is already claimed by an earlier regex
              // (typically a duplicate of the same pattern in the
              // highlight list), advance to the next literal occurrence
              // of the captured text past the taken range, bounded to
              // the same line — line-anchored regexes like
              // `^@issue:[^\n]*?(TOKEN)` can't find a second match by
              // re-execution alone since the anchor only appears once.
              if (hasGroup && overlaps(start, end)) {
                const lineStart = text.lastIndexOf("\n", m.index) + 1;
                const lineEndIdx = text.indexOf("\n", m.index + m[0].length);
                const lineEnd = lineEndIdx === -1 ? text.length : lineEndIdx;
                let searchFrom = end - from;
                while (overlaps(start, end)) {
                  const next = text.indexOf(gText, searchFrom);
                  if (next === -1 || next < lineStart || next + gLen > lineEnd) {
                    start = -1;
                    break;
                  }
                  start = from + next;
                  end = start + gLen;
                  searchFrom = next + gLen;
                }
              }
              if (start < 0 || overlaps(start, end)) continue;
              taken.push({ start, end });
            }
          }
        }
        taken.sort((a, b) => a.start - b.start);
        const ranges: Range<Decoration>[] = taken.map((t) =>
          highlightMark.range(t.start, t.end),
        );
        return Decoration.set(ranges, true);
      }

      /** First match offset across all regexes over the whole doc. */
      firstMatch(view: EditorView): number {
        const regexes = view.state.field(patternsField, false) ?? [];
        const text = view.state.doc.toString();
        let earliest = -1;
        for (const re of regexes) {
          re.lastIndex = 0;
          const m = re.exec(text);
          if (m) {
            const gOff = m[1] !== undefined ? m[0].length - m[1].length : 0;
            const pos = m.index + gOff;
            if (earliest === -1 || pos < earliest) earliest = pos;
          }
        }
        return earliest;
      }
    },
    { decorations: (v) => v.decorations },
  );

  return [patternsField, plugin];
}
