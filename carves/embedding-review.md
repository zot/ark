# Embedding review: is nomic still the right default model?

Ark embeds every chunk, tag, and query with one local GGUF model, run on-device
through yzma's llama.cpp binding. The default has been `nomic-embed-text-v1.5`
(Q8_0) since vector search landed. A newer candidate, `jina-embeddings-v5-text-nano`,
claims better retrieval, and `ark update` is about to start downloading whichever
model the config names ([setup-and-update.md](setup-and-update.md)), so the
default is worth re-examining before it becomes something every install fetches.

The question is coherent rather than a single change because the answer depends
on things only a measurement on ark's own corpus can settle, and one of them
turned out to be a defect in how ark uses the model it already has.

## Status

- [ ] **Item 1 — a retrieval yardstick.** **OPEN (not queued.)** A fixed query
      set with known-relevant chunks, so every later change is measured the same
      way.
- [ ] **Item 2 — per-model prefixes and pooling.** **OPEN (not queued.)** ark
      sends nomic no task prefixes today; fixing that is worth doing whichever
      model wins.
- [ ] **Item 3 — the jina v5 nano bake-off.** **OPEN (not queued.)** Does it load
      and stay correct under yzma's pinned llama.cpp, how fast is it here, and
      does it beat a correctly prefixed nomic on the yardstick?
- [ ] **Item 4 — choose the default.** **OPEN (not queued.)** Bill's call, from
      Items 2 and 3.
- [ ] **Item 5 — a classifier for tagging (Laya).** **OPEN (not queued.)**
      Classifiers are a new tool in the toolbox (Bill, 2026-09-24): calibrated
      probabilities over a schema, beside embeddings' similarity. Weights are
      open; Python is allowed (Bill), so it can run now. Candidate uses: tag
      applicability and recall relevance, as a first pass ahead of the LLM.
      Benchmarked against Haiku, which judges relevance anyway and may make it
      moot (Bill, 2026-09-26).

<a id="findings"></a>
## What is known (2026-09-24)

From the model cards and llama.cpp's tracker, gathered by a research pass; each
figure was stated by its source and not reproduced here.

| | nomic-embed-text-v1.5 | jina-embeddings-v5-text-nano |
|---|---|---|
| License | Apache 2.0 | CC-BY-NC-4.0 (non-commercial; fine for personal use, per Bill) |
| Parameters | 137M | 239M, on EuroBERT-210m |
| Dimensions | 768, Matryoshka to 64 | 768, Matryoshka to 32 |
| Context | 8192; llama.cpp's GGUF path defaults to 2048 | 8192 |
| Pooling | mean | last token |
| llama.cpp architecture | `nomic-bert`, mature since early 2024 | `eurobert`, recent |
| GGUF | one official model, prefix-switched; Q8_0 is 146,146,432 bytes | one official GGUF per task (retrieval, text-matching, clustering, classification); Q8_0 size not confirmed |
| Retrieval prompts | `search_query:` / `search_document:` prefixes | query / document prompts within the retrieval GGUF |
| Scores | MTEB overall 62.28; no clean retrieval-only figure found | MTEB retrieval 63.33, BEIR nDCG@10 56.06 |
| Released | 2024-02 | 2026-02-18 |

**The quality edge is not established.** Jina's 63.33 is a retrieval average and
nomic's 62.28 is an overall average; they are not the same measurement.

**A caution on the GGUF path.** jina's sibling `v5-text-small` (a different
architecture, Qwen3-based) produces wrong GGUF embeddings past about 512 tokens
in llama.cpp, an issue closed as not planned. That does not implicate nano, but
nothing confirms nano's long-input correctness under llama.cpp either.

## What the code assumes

- **No task prefixes are sent.** Nothing in `embed.go` or `librarian.go`
  prepends `search_query:` or `search_document:`, though nomic's card says
  retrieval requires them. ark may be under-using its current model.
- **Pooling is fixed to mean** (`embed.go`, the context parameters), which is
  right for nomic and wrong for jina's last-token pooling.
- **llama.cpp is pinned** (`[embedding] llama_version`, within yzma's tested
  range). Whether that range includes `eurobert` support is not verified.
- **A model change drops every embedding** (`db.go`, the model-changed config
  path), so a switch re-embeds the whole corpus. On the dev box that is 8,190
  files and 1.9 GB; for a new install it costs nothing.
- **Some tuning assumes nomic's distribution.** `AboutRequest`'s comment in
  `librarian.go` records that a cosine-threshold reducer was found to be a no-op
  against nomic's embedding distribution (R1928). Other thresholds may carry the
  same assumption and would need re-checking after a switch.

<a id="1"></a>
## Item 1

A set of real queries against the dev-box corpus, each with the chunks a person
judged relevant, scored the same way every time (recall at K, or nDCG). Without
it, Items 2 and 3 can only be judged by impression. It should run through ark's
own search path, not a side harness, so it measures what users get.

<a id="2"></a>
## Item 2

Make the retrieval prompts and the pooling type per-model settings in the
`[embedding]` config, with nomic's prefixes as its defaults. Measure the
yardstick before and after.

**The prefix depends on the use, not only the model (2026-09-26).** nomic v1.5
has four task prefixes: `search_query:`, `search_document:`, `classification:`,
and `clustering:`. Search is asymmetric (query against document), but matching
tag definitions against chunks is symmetric, so `search_query:` on a tag
definition is likely wrong, and `classification:` on both sides is the natural
candidate. The config therefore needs a prefix per use (search, tag matching)
for each model, and the yardstick should compare the candidates for tag
matching rather than assume one. The point surfaced in a Gemini exchange,
which suggested `search_document:` on both sides; nomic's own
`classification:` prefix is the stronger candidate to test. This is independent of Item 3: a correctly prompted
nomic is also the fair baseline jina has to beat.

<a id="3"></a>
## Item 3

Four checks, in order, each able to end the trial:

1. The retrieval GGUF loads under yzma's pinned llama.cpp.
2. Embeddings stay correct past 512 tokens (compare against the model's
   reference implementation on a few long chunks).
3. Throughput on the dev box, since the model is about 1.7 times nomic's size
   and every chunk pays for it.
4. The yardstick, against Item 2's prefixed nomic.

<a id="4"></a>
## Item 4

Whichever model wins, `ark update` stays model-neutral: it downloads the model
the config names, from a per-model URL, checked against a pinned SHA-256. So
this carve never blocks setup-and-update; it only changes which entry the
bundled `ark.toml` carries.

<a id="5"></a>
## Item 5

**DECIDED (Bill, 2026-09-24): classifiers are a new tool in the toolbox, and
Laya is the candidate for tagging.** An embedding says how *similar* two things
are; a classifier says how *likely* a label is, as a calibrated probability.
Tagging wants the second, and ark has so far approximated it with the first.

What is known, from a 2026-09-24 write-up of the release (not from the model
itself): Laya is an Apache 2.0 classifier from convaiinnovations built on a
bidirectional encoder. It scores text against a user-defined schema and returns
calibrated probabilities, not generated text, through three primitives:
`choice` (categorical), `score` (ordinal), and `noul` (calibrated binary). It
ships English, multilingual, and routing checkpoints, and claims results in
under 35 ms. The write-up says accuracy degrades once a schema has more than
about twenty options.

**Artifacts, found 2026-09-24.** The weights are open: Hugging Face
`convaiinnovations/laya`, also on PyPI (`laya`) and GitHub
(`NandhaKishorM/laya`). The English checkpoint is a ModernBERT-large backbone
(395M) with a small decision head, 421M parameters and about 810 MB of
safetensors; the multilingual one is mmBERT-base, 322M parameters. The schema is
supplied at inference time as typed questions with per-option criteria text, so
no retraining per schema, though it is a purpose-trained decision head rather
than label-text zero-shot. Its calibration figure (ECE 0.081 after temperature
scaling) is the vendor's own; no independent benchmark of it exists.

**What the README and source say (checked 2026-09-26).** Read from the Hugging
Face README and `laya/agent.py` on GitHub, not from running it:

- **Context is small.** The English checkpoint reads 512 tokens per question,
  of which `head_max_len = 192` is reserved for the question and its options,
  leaving roughly 320 for the text being judged. The multilingual checkpoint
  defaults to 1024 (`head_max_len = 256`) and is stated to support up to 8192.
  Many ark chunks exceed 320 tokens, so the English checkpoint would truncate
  them.
- **The option budget explains the twenty-option limit.** All of a `choice`
  question's options share `head_max_len`, criteria text included; at 77
  options the README counts 3 to 4 tokens per label. Past 50 options it
  recommends a larger `head_max_len` or two-step routing.
- **`choice` returns every option's score.** `predict(state, questions)`
  answers a `choice` question with `choice` (the argmax), `probabilities` (one
  value per option), `confidence`, and `answer_confidence`. The probabilities
  are a single temperature-scaled softmax, so they sum to one within the
  question, and the head is trained to pick one answer. Reading several tags
  off one `choice` result is therefore off-label, which is the risk the
  relative cutoff and the 80/20 rule below exist to contain.
- **Gate on `answer_confidence`.** For `choice` and `score`, `confidence` is a
  normalized entropy; `answer_confidence` is the calibrated quantity on every
  question type, the one temperature scaling fits and ECE measures.
- **All questions in a `predict` call share one forward pass**, but each
  question is paired with the state, so many `noul` questions still cost many
  encodings of the chunk. That is the price of independent per-tag scores.
- **Embedding shortlisting is built in.** `laya/shortlist.py` ranks a large
  option set by cosine similarity from a caller-supplied `embed_fn`, keeps the
  top `k` (default 20), and runs one `choice` over them. nomic could be that
  `embed_fn`.
- **The published latency is on a GPU:** 39.5 ms for one English question on a
  T4, 103 to 332 questions per second batched. Nothing speaks to a CPU.
- The package also carries an `onnx_agent.py`, a possible route that avoids
  PyTorch; not examined.

**It cannot run under yzma today.** Only safetensors and PyTorch are published,
no GGUF. Mainline llama.cpp has run ModernBERT since December 2025 (PR #15641),
but as an embedding encoder without a classification head. A dedicated Laya
architecture with its decision head was proposed in llama.cpp PR #29363, opened
2026-09-24 by a third-party contributor and unreviewed.

**DECIDED (Bill, 2026-09-24): ark may use Python for this.** So the llama.cpp
path is an optimization for later, not a blocker: Laya can run now through its
own Python package, as a helper process ark manages. What that costs is a Python
environment `setup` has to provision on Linux and Windows alike, a sidecar
lifecycle, and a second resident model beside the embedding model. Its speed on
the dev box's CPU is unmeasured.

**Two uses, both now done by an LLM (Bill, 2026-09-24):**

- **Tag applicability.** Deciding whether any current tag applies to a piece of
  text, which the Go code, the Haiku secretaries, and luhmann do today.
- **Relevance.** Deciding whether a candidate chunk fits the live conversation,
  the recall secretary's judgment. One `noul` question per candidate, so the
  twenty-option limit does not bind. The context window does bind, though: the
  English checkpoint reads 512 tokens per question (see below), too few for a
  conversation excerpt and a chunk together, so this use points at the
  multilingual checkpoint.

**The likely shape is a first pass, not a replacement.** The LLM does more than
decide: it sharpens tags and writes up what it surfaces, which Laya cannot. But
much of the LLM's cost is spent on candidates it ends up discarding. Laya
scoring every candidate and passing only the survivors on would take that cost
without giving up the writing. Whether it is faster and cheaper in practice is
the measurement, not an assumption.

**Picking past the twenty-option limit (Bill, 2026-09-24).** Laya degrades past
about twenty options, and ark's tag vocabulary is larger, so the tags are split
into groups of twenty and each group is classified once against the text. The
candidates are then drafted round-robin from those scores:

1. Move the top remaining tag from each group into the candidate list, provided
   it exceeds the cutoff.
2. Remove the groups with no remaining tag above the cutoff.
3. Take another pass while the candidates plus the remaining groups number
   twenty or fewer, so the list never exceeds twenty.

A final classification over the candidates decides which apply. This handles up
to twenty groups of twenty (400 tags) in one pass per group plus the final one.

**It never compares scores across groups**, which is its main strength: only a
tag's rank within its own group and the cutoff are used, so a strong tag in a
crowded group is still its group's first pick. That matters because a `choice`
score is a distribution that sums to one within its group (confirmed from the
source 2026-09-26), and such scores are not comparable between groups.

**DECIDED (Bill, 2026-09-24): the cutoff is relative**, a multiple of the
group's uniform share or a margin over its none-of-these option, so several
applicable tags splitting one group's mass cannot all fall below it.

**DECIDED (Bill, 2026-09-24): the last step is one classification against all
the candidates.** Several tags can apply to a chunk, so the result is read by
shape: a clear winner is taken alone; otherwise the top tags whose scores sit
close together are all taken. When no tag clears the relative cutoff, the chunk
gets no tag.

**DECIDED (Bill, 2026-09-24): "close" is Pareto, 80/20.** Take the fewest top
tags whose scores sum to 80% of the total. That subsumes the clear-winner case,
since a top tag at 0.8 or more is taken alone. One guard: on a flat
distribution, reaching 80% could take most of the candidates, which means no
tag stands out rather than that they all apply, so each tag taken must still
clear the relative cutoff, and a flat result yields no tag. The 80% figure is a
starting point for the trial to confirm.

Past 400 tags the same drafting can run on groups of groups, or embeddings can
shortlist first.

**There is no labeled history (Bill, 2026-09-24).** Neither the secretaries'
surface-or-skip decisions nor the Tag Forge's accept/reject outcomes are
recorded, so any evaluation set is built by hand. Recording those decisions from
now on would grow one as a side effect; that is a suggestion, not a decision.

**Haiku is the baseline to beat (Bill, 2026-09-26).** A Haiku pass judges
relevance before results go back to the requestor in any case, and it already
holds all the tag definitions. If it can pick candidate tags quickly in that
same pass, a classifier (Laya) or a new embedding model (jina v5) adds a second
resident model and a Python sidecar to save work Haiku was doing anyway, and may
be moot. So each is benchmarked against Haiku, and earns a place only by beating
it on latency or cost at comparable quality.

**First step: a hands-on trial, before any design.** Classification is new to
the project, so everything above (the two uses, the first-pass shape, the
winnowing) is a sketch to test rather than a design to build. The trial runs a
dozen real chunks and ten to twenty hand-picked tags three ways: Haiku with the
tag definitions, Laya's Python package in a scratch environment, and nomic
cosine similarity as today's reference. A person reads the results. It should
show:

- whose calls resemble a person's, and how each treats "none of these";
- latency per chunk on the dev box's CPU for Laya, beside Haiku's wall-clock
  and token cost;
- for Laya, independent `noul` questions per tag against `choice` groups read
  through the winnowing, on the same chunks: `noul` gives comparable
  calibrated scores and needs no cutoff machinery, but costs an encoding per
  tag;
- how much truncation at the English checkpoint's ~320 tokens hurts, against
  the multilingual checkpoint;
- whether tag criteria phrased as has-a ("the text uses a programming
  language") beat is-a ("a programming language"). Bill and the Gemini
  exchange below both expect has-a to win.

Its reasoning goes in `.scratch/` with the script, stamped with when it ran and
against what.

It shares this carve's yardstick question: Item 1's judged set could be extended
with tag judgments, so a classifier's suggestions are measured rather than
admired.

### Gemini speculation about using Laya

Kept verbatim. Checked against the README and source on 2026-09-26, with the
findings folded in above. Three corrections apply to the answer as written. The
"~40ms per batch" is the vendor's T4 GPU figure, not a Steam Deck measurement.
`laya_client.evaluate(..., type="noul")` is not Laya's API; the call is
`agent.predict(state, questions)` with the type on each question. And `choice`
does score every option, though as one softmax trained for a single answer.

#### Question:
I have nomic-embed-text-v1.5 and they are embedded. I am currently using cosine similarity and thinking about trying laya.

Multiple tags can apply, like "programming-language: C" and "program-type: video game". My tags have values that are assigned elsewhere, so the tags laya would check for are "programming-language" and "program-type" in this case. The "programming-language" tag description is something like "a programming language" but probably needs rephrasing to indicate the "has-a" relationship to something like, "uses a programming language".

#### Gemini's answer:
Transitioning from nomic-embed-text-v1.5 cosine similarity to Laya changes your architecture from mapping absolute semantic similarity to checking specific categorical conditions.
Because you are checking for structural meta-categories ("programming-language", "program-type") rather than matching the variable values themselves (like "C" or "video game"), Laya is ideal for this task—but you must format your prompts carefully due to its strict token limitations.
1. Rewriting Your Prompts for a "Has-A" Relationship
Cosine similarity matches text based on overlapping semantic overlap. Laya's Noul operator requires a strict binary statement to verify. As you noted, "a programming language" is too vague. You must explicitly instruct the model to look for the presence or context of that attribute.
• Incorrect (Too abstract): "a programming language" (Laya will look for a snippet that is a programming language, like a code block).
• Correct (Has-A structure): "The text explicitly mentions or implies the use of a specific programming language (e.g., C, Python, JavaScript)."
• Correct (For program-type): "The text describes the specific nature, medium, or type of software program being built or used (e.g., video game, web application, database)."
2. The Multi-Label "Bottleneck" on the Steam Deck
If you run Laya locally on your Steam Deck, evaluating 108 separate Noul tags will take a heavy toll.
• The Constraint: The English version of Laya maps a max token window of 512 tokens, while laya-multilingual allows up to 1,024. Furthermore, Laya limits its option/question head prompt space (head_max_len) to 192 to 256 tokens. [1] (https://huggingface.co/convaiinnovations/laya/blob/main/README.md)
• The Problem: Packing 108 tag descriptions into a single query will instantly overflow that token budget, causing severe truncation, muddy classifications, or execution crashes. [1] (https://huggingface.co/convaiinnovations/laya/blob/main/README.md)
3. How to Structure It
Since multiple tags can apply simultaneously, you cannot use a single Choice question (which forces one winner). Instead, you should divide your 108 tags into small batches of 5 to 10 binary Noul questions, running them through Laya sequentially or in parallel batches.
python

##### Group your 108 tags into logical micro-batches to fit the token budget
tag_batches = [
    {
        "has_programming_language": "The text mentions or implies the use of a specific programming language.",
        "has_program_type": "The text describes the specific medium or type of software program being discussed."
    },
    {
        "has_database_tech": "The text references a database engine or data storage technology.",
        "has_cloud_provider": "The text mentions cloud infrastructure like AWS, Azure, or GCP."
    }
    # ... continue for remaining batches
]

##### Process your chunk through Laya
assigned_metadata_categories = []
for batch in tag_batches:
    # Laya processes each batch of Noul statements in a single forward pass
    results = laya_client.evaluate(state=text_chunk, questions=batch, type="noul")
    
    for tag, probability in results.items():
        if probability > 0.75: # Threshold calibration
            assigned_metadata_categories.append(tag)
Use code with caution.
4. The Recommendation: Embeddings vs. Laya
• Keep Nomic for Value Assignment: Continue using nomic-embed-text-v1.5 to find the values (e.g., matching the text chunk against a massive index of 500 different programming languages to find "C"). Vector embeddings excel at high-cardinality semantic searches. [1] (https://www.algolia.com/blog/ai/cosine-similarity-what-is-it-and-how-does-it-enable-effective-and-profitable-recommendations)
• Use Laya for Metadata Discovery: Use Laya exclusively as a local gatekeeper to decide which downstream embedding lookups to trigger. If Laya answers True to "has_programming_language", your pipeline then routes that chunk to your Nomic vector search to pull out the exact language. This keeps your local inference pipeline extremely fast (~40ms per batch) while preventing token overflow. [1] (https://huggingface.co/convaiinnovations/laya), [2] (https://huggingface.co/convaiinnovations/laya/blob/main/README.md)
Would you like help writing a routing script that uses Laya to identify the active structural categories, and then dynamically queries your Nomic vector index for the specific values?
