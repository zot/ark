# Nano (embedded shell-agent loop)

Ark embeds a small shell-agent loop, callable as `ark nano`. It
reads a prompt, asks a local Ollama model what to do, runs shell
commands with the user's approval, and feeds the output back to
the model until it produces a final answer.

The integrated code is a Go port of nano.py. Both are MIT-licensed.

## Upstream — `nano.py`

- **Author:** Parham Negahdar ([pnegahdar](https://github.com/pnegahdar))
- **Repository:** https://github.com/pnegahdar/nano
- **License:** MIT
- **Shape:** single-file Python script, OpenAI Responses API,
  zero dependencies. Approximately 200 lines.

## Port — `nano-go`

- **Author:** Bill Burdick ([zot](https://github.com/zot))
- **Repository:** https://github.com/zot/nano-go
- **License:** MIT
- **Shape:** Go library + thin CLI. Backend swapped to Ollama
  (`/api/chat`, stateless). One non-stdlib dependency:
  `github.com/chzyer/readline`, used by the REPL only.

The port keeps nano.py's one-tool philosophy, 5–10-word command
description, 200-step loop cap, 12 KB output cap, y/a/n approval
flow, and the system prompt that lists project documentation and
discovered skill files. Backend, session persistence, and object
shape are the three changes — documented in
[`specs/nano-overview.md`](specs/nano-overview.md).

## Integration into Ark

The Go source from `nano-go` lives at `nano.go` in the ark
package. The CLI surface is exposed as `ark nano`. The
mini-spec design is folded into ark's design tree (`crc-Nano*.md`,
`seq-nano-*.md`, `test-Nano*.md`); nano's R1–R74 are renumbered
into ark's R-space as R2486–R2559.

The MIT terms of both the upstream and the port carry forward.
This document records the chain of authorship and the license
under which the integrated copy ships in ark.

## License

```
MIT License

Copyright (c) 2025 Parham Negahdar (nano.py)
Copyright (c) 2026 Bill Burdick (nano-go port of nano.py)

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without
restriction, including without limitation the rights to use,
copy, modify, merge, publish, distribute, sublicense, and/or
sell copies of the Software, and to permit persons to whom the
Software is furnished to do so, subject to the following
conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.
```
