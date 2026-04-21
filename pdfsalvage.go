package ark

// CRC: crc-PDFChunker.md | Seq: seq-pdf-salvage.md | R1652-R1660

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"github.com/zot/microfts2"
)

// salvageChunks is the best-effort text extractor used when seehuhn's
// pdf.NewReader rejects a file. Walks the raw bytes for content streams,
// decodes FlateDecode where present, extracts Tj / TJ / ' / " text,
// and returns one chunk per stream that produced text. R1652, R1657, R1658
func salvageChunks(content []byte) []microfts2.Chunk {
	var chunks []microfts2.Chunk
	streamN := 0
	i := 0
	for i < len(content) {
		j := bytes.Index(content[i:], []byte("stream"))
		if j < 0 {
			break
		}
		dictEnd := i + j
		streamStart := dictEnd + len("stream")
		streamStart = skipEOL(content, streamStart)

		k := bytes.Index(content[streamStart:], []byte("endstream"))
		if k < 0 {
			break
		}
		streamEnd := streamStart + k
		streamEnd = trimTrailingEOL(content, streamStart, streamEnd)

		dict := precedingDict(content, dictEnd)
		decoded, ok := decodeStream(content[streamStart:streamEnd], dict) // R1654
		if ok {
			text := extractStreamText(decoded) // R1655
			if strings.TrimSpace(text) != "" {
				streamN++
				chunks = append(chunks, microfts2.Chunk{
					Range:   []byte(fmt.Sprintf("salvage/%d", streamN)),
					Content: []byte(strings.TrimSpace(text)),
					Attrs: []microfts2.Pair{
						{Key: []byte("page"), Value: []byte("0")}, // R1658, R1723
					},
				})
			}
		}

		i = streamEnd + len("endstream")
	}
	return chunks
}

// skipEOL advances past a single CRLF, LF, or CR at index i.
func skipEOL(b []byte, i int) int {
	if i < len(b) && b[i] == '\r' {
		i++
	}
	if i < len(b) && b[i] == '\n' {
		i++
	}
	return i
}

// trimTrailingEOL returns end with any trailing CR/LF stripped, but
// never crossing the start boundary.
func trimTrailingEOL(b []byte, start, end int) int {
	if end > start && b[end-1] == '\n' {
		end--
	}
	if end > start && b[end-1] == '\r' {
		end--
	}
	return end
}

// precedingDict returns the object dictionary bytes that immediately
// precede dictEnd (where "stream" begins). The dictionary is the
// span between the nearest preceding "<<" and the ">>" just before
// "stream". If we can't locate one cleanly, returns nil — the caller
// treats that as "no filter".
func precedingDict(b []byte, dictEnd int) []byte {
	closeIdx := bytes.LastIndex(b[:dictEnd], []byte(">>"))
	if closeIdx < 0 {
		return nil
	}
	openIdx := bytes.LastIndex(b[:closeIdx], []byte("<<"))
	if openIdx < 0 {
		return nil
	}
	return b[openIdx:closeIdx]
}

// decodeStream applies the filter declared in dict (if any) to stream
// bytes. Supports no filter and /FlateDecode; other filters return
// (nil, false) so the stream is skipped. R1654
func decodeStream(stream, dict []byte) ([]byte, bool) {
	if dict == nil || !bytes.Contains(dict, []byte("/Filter")) {
		return stream, true
	}
	if bytes.Contains(dict, []byte("/FlateDecode")) {
		return flateDecode(stream)
	}
	return nil, false
}

func flateDecode(data []byte) ([]byte, bool) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, false
	}
	return out, true
}

// extractStreamText walks a decoded content stream and concatenates
// the string arguments of the Tj, TJ, ', and " text-showing
// operators, separated by newlines between distinct operators.
// R1655
func extractStreamText(stream []byte) string {
	var out strings.Builder
	i := 0
	for i < len(stream) {
		i = skipWhitespace(stream, i)
		if i >= len(stream) {
			break
		}
		switch stream[i] {
		case '(':
			s, end := readPDFString(stream, i)
			if end < 0 {
				i++
				continue
			}
			j := skipWhitespace(stream, end)
			switch op := peekOperator(stream, j); op {
			case "Tj":
				out.WriteString(s)
				out.WriteByte('\n')
				i = j + 2
			case "'", `"`:
				out.WriteString(s)
				out.WriteByte('\n')
				i = j + 1
			default:
				i = end
			}
		case '[':
			end, texts := readPDFArray(stream, i)
			if end < 0 {
				i++
				continue
			}
			j := skipWhitespace(stream, end)
			if peekOperator(stream, j) == "TJ" {
				for _, t := range texts {
					out.WriteString(t)
				}
				out.WriteByte('\n')
				i = j + 2
			} else {
				i = end
			}
		default:
			i++
		}
	}
	return out.String()
}

// peekOperator returns the 1- or 2-byte operator at j, or "".
func peekOperator(stream []byte, j int) string {
	if j >= len(stream) {
		return ""
	}
	c := stream[j]
	if c == '\'' {
		return "'"
	}
	if c == '"' {
		return `"`
	}
	if j+1 < len(stream) {
		two := string(stream[j : j+2])
		if two == "Tj" || two == "TJ" {
			return two
		}
	}
	return ""
}

// readPDFString reads a PDF string literal starting at stream[i]
// (which must be '('). Returns the decoded string and the byte
// index just past the closing ')'. Returns ("", -1) on malformed.
// Handles balanced nested parens and PDF escape sequences. R1656
func readPDFString(stream []byte, i int) (string, int) {
	if i >= len(stream) || stream[i] != '(' {
		return "", -1
	}
	var sb strings.Builder
	depth := 1
	i++
	for i < len(stream) && depth > 0 {
		c := stream[i]
		switch {
		case c == '\\' && i+1 < len(stream):
			next := stream[i+1]
			switch next {
			case 'n':
				sb.WriteByte('\n')
				i += 2
			case 'r':
				sb.WriteByte('\r')
				i += 2
			case 't':
				sb.WriteByte('\t')
				i += 2
			case 'b':
				sb.WriteByte('\b')
				i += 2
			case 'f':
				sb.WriteByte('\f')
				i += 2
			case '(', ')', '\\':
				sb.WriteByte(next)
				i += 2
			case '\n':
				i += 2
			case '\r':
				i += 2
				if i < len(stream) && stream[i] == '\n' {
					i++
				}
			default:
				if next >= '0' && next <= '7' {
					val := 0
					j := i + 1
					for k := 0; k < 3 && j < len(stream) && stream[j] >= '0' && stream[j] <= '7'; k++ {
						val = val*8 + int(stream[j]-'0')
						j++
					}
					sb.WriteByte(byte(val))
					i = j
				} else {
					sb.WriteByte(next)
					i += 2
				}
			}
		case c == '(':
			depth++
			sb.WriteByte(c)
			i++
		case c == ')':
			depth--
			if depth > 0 {
				sb.WriteByte(c)
			}
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	if depth != 0 {
		return "", -1
	}
	return sb.String(), i
}

// readPDFArray reads a TJ-style array starting at stream[i] (which
// must be '['). Returns the byte index past ']' and the collected
// string literals. Numbers (kerning) are ignored. Returns (-1, nil)
// on malformed. R1655
func readPDFArray(stream []byte, i int) (int, []string) {
	if i >= len(stream) || stream[i] != '[' {
		return -1, nil
	}
	i++
	var texts []string
	for i < len(stream) {
		i = skipWhitespace(stream, i)
		if i >= len(stream) {
			return -1, nil
		}
		if stream[i] == ']' {
			return i + 1, texts
		}
		if stream[i] == '(' {
			s, end := readPDFString(stream, i)
			if end < 0 {
				return -1, nil
			}
			texts = append(texts, s)
			i = end
			continue
		}
		// Skip number or other token up to next whitespace or delimiter.
		for i < len(stream) && !isDelim(stream[i]) {
			i++
		}
	}
	return -1, nil
}

func skipWhitespace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\n', '\r', '\f':
			i++
		default:
			return i
		}
	}
	return i
}

func isDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '(', ')', '[', ']', '<', '>', '/', '%':
		return true
	}
	return false
}
