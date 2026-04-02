// CRC: crc-ArkTagParser.md
import { tags as t } from "@lezer/highlight";
import type { MarkdownConfig, InlineContext } from "@lezer/markdown";

/** Tag pattern: @word: value. Colon disambiguates from emails. */
export const ARK_TAG_PATTERN = /^@([\w][\w-]*):[ \t]*(.*)/;

/**
 * Lezer markdown extension that recognizes @tag: value patterns
 * and produces ArkTag nodes.
 */
export const arkTagExtension: MarkdownConfig = {
  defineNodes: [
    { name: "ArkTag", style: t.special(t.meta) },
    { name: "ArkTagName", style: t.tagName },
    { name: "ArkTagValue", style: t.string },
  ],
  parseInline: [
    {
      name: "ArkTag",
      before: "Link",
      parse(cx: InlineContext, next: number, pos: number): number {
        if (next !== 64 /* @ */) return -1;

        const text = cx.slice(pos, cx.end);
        const match = text.match(ARK_TAG_PATTERN);
        if (!match) return -1;

        const tagName = match[1];
        const tagValue = match[2];
        const fullLength = match[0].length;

        const nameStart = pos + 1;
        const nameEnd = nameStart + tagName.length;
        // Value starts after "@name: " — find it directly from match
        const valueStart = pos + match[0].indexOf(match[2], tagName.length + 2);
        const valueEnd = pos + fullLength;

        const children = [
          cx.elt("ArkTagName", nameStart, nameEnd),
        ];
        if (tagValue.length > 0) {
          children.push(cx.elt("ArkTagValue", valueStart, valueEnd));
        }

        cx.addElement(cx.elt("ArkTag", pos, pos + fullLength, children));
        return pos + fullLength;
      },
    },
  ],
};
