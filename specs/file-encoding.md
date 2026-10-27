# File encoding

ark indexes text files whatever their encoding, as long as the encoding says
what it is. Language: Go. Environment: the indexer, the chunker registration
seam, and the places that read or write a user's file as text.

## Why

On Windows the ordinary way to make a text file can make a UTF-16 one.
`echo hello > bubba.md` in Windows PowerShell 5, the shell Windows ships,
writes UTF-16LE with a byte-order mark, and so does Notepad when "Unicode" is
picked. Measured 2026-10-27: ark skipped such a file as containing invalid
UTF-8, so a user's notes were silently missing from the index.

## What ark reads

A file's encoding is read from its byte-order mark:

- `FF FE` — UTF-16 little-endian.
- `FE FF` — UTF-16 big-endian.
- anything else — UTF-8, exactly as before.

A file that starts with the UTF-8 byte-order mark (`EF BB BF`) is UTF-8 and is
read as it always has been, mark included. Treating it as a separate encoding
would shift the offsets of every such file already in the index, and nothing
would re-index them, since their bytes have not changed.

ark does not guess. A UTF-16 file with no byte-order mark is not detected and
is read as UTF-8, which it is not, so it is skipped as before.

## Chunks are UTF-8

Every chunk's content is UTF-8, whatever the file's encoding. A UTF-16 file is
decoded before it is chunked, so one chunker serves every encoding and the
chunkers themselves never learn about encodings. A chunk's line range is the
same in either encoding; its byte locator counts bytes of the decoded text.

Every text chunker decodes. microfts2's markdown, bracket and indent chunkers
are wrapped at ark's chunker registration, as ark already wraps them for inline
tags; the `lines` strategy is a plain function, wrapped the same way; ark's own
chat-log and scripture chunkers decode at their own entry, since ark calls them
directly as well as through microfts2. The PDF chunker reads a binary format,
which never starts with a text byte-order mark. microfts2 is unchanged: every
path by which it reads a text file hands the bytes to the chunker first.

A file that is not UTF-8 is never indexed by appending its new tail; a change
to it re-indexes the whole file. Appending works on the raw bytes, and chat
logs, the files appending exists for, are UTF-8.

The external-command strategies (`lines-overlap`, `words-overlap`) run a
separate program that opens the file itself, so for now they read UTF-8 only.
When one meets a UTF-16 file, the message names the encoding rather than
calling the file invalid UTF-8.

## The encoding is recorded per file

ark records the encoding of each indexed file that is not UTF-8, keyed by the
file's id, and whether the file can be written back. A UTF-8 file has no
record, which is every file indexed before this. The record is removed with the
file, and moves with it when a re-index gives the file a new id.

## Writing a file back

Everything that rewrites a user's file as text — saving from the editor,
setting a file's tags (from the editor, `ark tag set`, or the apps), inserting
or removing an inline tag, the curation editor's region edit, and rescheduling
a tag — reads the file as text, edits the text, and writes it back
in the file's own encoding, byte-order mark included. The encoding is read
again from the file at the moment of writing, not taken from the record, so a
file re-saved as UTF-8 since it was indexed is not converted back.

A file is writable only if it survives the round trip: encoding its decoded
text reproduces the file exactly. A malformed UTF-16 file, such as one holding
an unpaired surrogate, decodes with replacement characters and would come back
different, so ark indexes it and does not write it. The editor is not offered
for it, and a write is refused rather than altering bytes the user did not
touch.

## Reading a file for display

The content view and `/fetch` show the decoded text, and so does everything
else that reads a file's tags outside the index: `ark tag get` and `ark tag
check`, and the messaging inbox and message reader. `/raw` returns the file's
bytes unchanged, as its name says.
