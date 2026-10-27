package ark

// A user's file may be UTF-16 (Windows PowerShell 5's `>` writes it); every
// chunker, tag scanner and browser here speaks UTF-8. This file is the edge
// where the two meet (specs/file-encoding.md).

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zot/microfts2"
)

// FileEncoding names a file's text encoding.
type FileEncoding string

const (
	EncodingUTF8    FileEncoding = "utf-8"
	EncodingUTF16LE FileEncoding = "utf-16le"
	EncodingUTF16BE FileEncoding = "utf-16be"
)

var (
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// errNotWritable refuses a write to a file that would not survive it.
var errNotWritable = errors.New("not writable: its text does not survive re-encoding")

// DetectEncoding returns the encoding data's byte-order mark names. Anything
// without a UTF-16 mark is UTF-8, the UTF-8 mark included: such files are
// indexed with the mark today, and stripping it would shift their locators.
// CRC: crc-FileEncoding.md | R3656
func DetectEncoding(data []byte) FileEncoding {
	switch {
	case bytes.HasPrefix(data, bomUTF16LE):
		return EncodingUTF16LE
	case bytes.HasPrefix(data, bomUTF16BE):
		return EncodingUTF16BE
	}
	return EncodingUTF8
}

// DecodeText returns data as UTF-8 text, its encoding, and whether encoding
// the text again reproduces data exactly. UTF-8 data is returned as is.
// CRC: crc-FileEncoding.md | R3657, R3661
func DecodeText(data []byte) (text []byte, enc FileEncoding, roundTrips bool) {
	enc = DetectEncoding(data)
	if enc == EncodingUTF8 {
		return data, enc, true
	}
	body := data[2:]
	units := make([]uint16, len(body)/2)
	for i := range units {
		lo, hi := body[2*i], body[2*i+1]
		if enc == EncodingUTF16BE {
			lo, hi = hi, lo
		}
		units[i] = uint16(lo) | uint16(hi)<<8
	}
	runes := utf16.Decode(units)
	text = make([]byte, 0, len(runes))
	for _, r := range runes {
		text = utf8.AppendRune(text, r)
	}
	return text, enc, bytes.Equal(EncodeText(text, enc), data)
}

// EncodeText returns UTF-8 text in enc, byte-order mark included; UTF-8 text
// is returned as is.
// CRC: crc-FileEncoding.md | R3662
func EncodeText(text []byte, enc FileEncoding) []byte {
	if enc == EncodingUTF8 {
		return text
	}
	units := utf16.Encode(bytes.Runes(text))
	out := make([]byte, 2, 2+2*len(units))
	copy(out, bomUTF16LE)
	if enc == EncodingUTF16BE {
		copy(out, bomUTF16BE)
	}
	for _, u := range units {
		lo, hi := byte(u), byte(u>>8)
		if enc == EncodingUTF16BE {
			lo, hi = hi, lo
		}
		out = append(out, lo, hi)
	}
	return out
}

// ReadText reads a file as UTF-8 text for a reader that does not write it
// back, so a file that would not survive re-encoding is still read.
// CRC: crc-FileEncoding.md | R3663
func ReadText(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text, _, _ := DecodeText(data)
	return text, nil
}

// ReadEditableText reads a file as text for an edit: its UTF-8 text and the
// encoding to write it back in, or errNotWritable when it would not survive.
// CRC: crc-FileEncoding.md | R3661, R3662
func ReadEditableText(path string) ([]byte, FileEncoding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	text, enc, ok := DecodeText(data)
	if !ok {
		return nil, enc, fmt.Errorf("%s is %s and %w", path, enc, errNotWritable)
	}
	return text, enc, nil
}

// EncodeForFile returns text in the encoding the file at path has now, for a
// write that replaces it without reading it first (an editor save). A file
// that does not exist yet is written as UTF-8.
// CRC: crc-FileEncoding.md | R3662
func EncodeForFile(path string, text []byte) ([]byte, error) {
	_, enc, err := ReadEditableText(path)
	if errors.Is(err, fs.ErrNotExist) {
		return text, nil
	}
	if err != nil {
		return nil, err
	}
	return EncodeText(text, enc), nil
}

// decodingChunker hands a UTF-16 file to the chunker it wraps as UTF-8, on
// every path microfts2 reads a text file: Chunks, GetChunk, FileChunks. Line
// ranges and byte locators are then those of the decoded text. UTF-8 data
// reaches the wrapped chunker untouched. AppendChunks promotes: the indexer
// never appends to a file that is not UTF-8 (R3658).
// CRC: crc-FileEncoding.md | R3657
type decodingChunker struct {
	fatChunker
}

// wrapDecoding wraps a fatChunker so it reads any encoding; any other chunker
// is returned unchanged.
// CRC: crc-FileEncoding.md | R3657
func wrapDecoding(c any) any {
	if fc, ok := c.(fatChunker); ok {
		return decodingChunker{fatChunker: fc}
	}
	return c
}

func (d decodingChunker) Chunks(path string, data []byte, yield func(microfts2.Chunk) bool) error {
	text, _, _ := DecodeText(data)
	return d.fatChunker.Chunks(path, text, yield)
}

// decodingFunc wraps a strategy function, such as `lines`, so it reads any
// encoding. A function strategy has no optional interfaces to mirror.
// CRC: crc-FileEncoding.md | R3664
func decodingFunc(fn microfts2.ChunkFunc) microfts2.ChunkFunc {
	return func(path string, content []byte, yield func(microfts2.Chunk) bool) error {
		text, _, _ := DecodeText(content)
		return fn(path, text, yield)
	}
}

// decodedFile is the GetChunk customData slot for a decoded file: the raw
// bytes it was decoded from, the text, and the wrapped chunker's own slot.
type decodedFile struct {
	raw   []byte
	text  []byte
	inner any
}

// GetChunk decodes once per file: microfts2's chunk cache passes the same
// bytes and slot on every call, so the text is cached in the slot.
func (d decodingChunker) GetChunk(path string, data []byte, customData *any, chunk *microfts2.Chunk) error {
	if DetectEncoding(data) == EncodingUTF8 {
		return d.fatChunker.GetChunk(path, data, customData, chunk)
	}
	df, _ := (*customData).(*decodedFile)
	if df == nil || len(df.raw) != len(data) || (len(data) > 0 && &df.raw[0] != &data[0]) {
		text, _, _ := DecodeText(data)
		df = &decodedFile{raw: data, text: text}
		*customData = df
	}
	return d.fatChunker.GetChunk(path, df.text, &df.inner, chunk)
}

// FileChunks reads the file itself, as microfts2's own text chunkers do: the
// hash is of the raw bytes, so the staleness short-circuit agrees with the
// content hash the split stores.
func (d decodingChunker) FileChunks(path string, old [32]byte, yield func(microfts2.Chunk) bool) ([32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, err
	}
	if len(data) == 0 {
		return [32]byte{}, nil
	}
	hash := sha256.Sum256(data)
	if old != ([32]byte{}) && hash == old {
		return hash, nil
	}
	return hash, d.Chunks(path, data, yield)
}

// microfts2InvalidUTF8 is the phrase microfts2's split puts in the error for a
// chunk that is not UTF-8; it has no typed error for the case.
const microfts2InvalidUTF8 = "invalid UTF-8"

// explainEncoding names the encoding when a chunker that reads UTF-8 only (one
// not behind decodingChunker) rejects a file that says it is UTF-16. Anything
// else is returned unchanged.
// CRC: crc-FileEncoding.md | R3659
func explainEncoding(err error, data []byte, strategy string) error {
	enc := DetectEncoding(data)
	if err == nil || enc == EncodingUTF8 || !strings.Contains(err.Error(), microfts2InvalidUTF8) {
		return err
	}
	return fmt.Errorf("the file is %s and the %s chunker reads UTF-8 only: %w", enc, strategy, err)
}
