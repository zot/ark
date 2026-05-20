# NanoOllamaClient
**Requirements:** R2487, R2564

The HTTP boundary. Marshals the chat request, posts to Ollama, parses
the assistant message back out. Distinct from Nano so the protocol surface
has a single home and tests can fake one without faking the whole agent.

## Knows
- BaseURL (from Nano)
- Model (from Nano)
- HTTPClient (from Nano)
- The Ollama chat request and response shapes
- The execute_shell tool definition that ships with every request

## Does
- Build a chat request: model, full message history, tools array, stream
  disabled
- POST to `<BaseURL>/api/chat` with Content-Type application/json
- On non-200 status, return an error containing the response body
- Decode the response into a Message and return it
- Start and stop the thinking spinner around the call when Nano.TTY is set

## Collaborators
- Nano: reads config and supplies the message history

## Sequences
- seq-nano-run-loop.md
