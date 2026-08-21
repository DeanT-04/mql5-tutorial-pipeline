// Package prompts holds every LLM prompt template. The system prompts are
// byte-stable shared prefixes: they must NEVER be interpolated with per-run
// data, so that Ollama's KV-cache prefix reuse applies within the keep-alive
// window (design.md §4.3). Per-chunk text goes only in the user message,
// which is appended after the stable prefix.
package prompts

// System is the byte-stable triage-pass system prompt.
const System = `You classify transcript chunks from MQL5 programming tutorials.

You will receive one transcript chunk. Decide whether it contains a concrete code action: the instructor writing, editing, adding, replacing, deleting, or dictating actual MQL5 code, or creating/configuring a source file.

Pure talk does NOT count: introductions, motivation, explanations of already-written code, marketing, outro, questions to viewers.

Answer with exactly one JSON object, no prose:
{"chunk_id": "<the chunk id you were given>", "has_code_action": true|false, "confidence": <0.0-1.0>}

Examples:

Chunk c0001: "Welcome back everyone, today we start a brand new series on building expert advisors in MetaTrader 5."
{"chunk_id":"c0001","has_code_action":false,"confidence":0.95}

Chunk c0002: "So let's add our first input, type input double LotSize equals 0.10, semicolon."
{"chunk_id":"c0002","has_code_action":true,"confidence":0.9}

Chunk c0003: "This function is called by the terminal every time a new tick arrives."
{"chunk_id":"c0003","has_code_action":false,"confidence":0.85}`

// DeepSystem is the byte-stable deep-extraction system prompt.
const DeepSystem = `You extract exact code-editing events from transcript chunks of MQL5 programming tutorials.

You will receive one transcript chunk that has already been flagged as containing a code action. Convert what the instructor writes or dictates into one or more structured events.

Event object fields:
- chunk_id: echo the id you were given
- seq: 1-based order within this chunk
- op: one of "create", "append", "replace", "property", "include"
  - create: a new file is started; code is its full initial content
  - append: lines are added to the end of the named file
  - replace: an existing snippet is replaced; anchor is the exact current snippet being replaced
  - property: a #property directive line; code contains the full directive
  - include: an #include directive line; code contains the full directive
- file: target file name only (no directories), always ending in .mq5
- anchor: for replace, the exact existing snippet; otherwise empty
- code: the exact MQL5 code, preserving spelling, casing, numbers, punctuation

Rules:
- Transcribe code EXACTLY as spoken/shown. Never fix, reformat, or invent code.
- If the instructor dictates a change to an earlier snippet, emit replace with the anchor copied verbatim from what they say is being replaced.
- Emit no events if the chunk turns out to contain no concrete code action.
- Output exactly one JSON object, no prose: {"events":[ ... ]}

Examples:

Chunk c0007: "Create a new file called MyEA.mq5, and we begin with the property strict directive at the top."
{"events":[{"chunk_id":"c0007","seq":1,"op":"create","file":"MyEA.mq5","anchor":"","code":"#property strict"}]}

Chunk c0011: "Below OnInit let's add our input, input double LotSize equals zero point one oh, with a semicolon."
{"events":[{"chunk_id":"c0011","seq":1,"op":"append","file":"MyEA.mq5","anchor":"","code":"input double LotSize = 0.10;"}]}

Chunk c0015: "Now find the line with the lot size input and change zero point one oh to zero point five."
{"events":[{"chunk_id":"c0015","seq":1,"op":"replace","file":"MyEA.mq5","anchor":"input double LotSize = 0.10;","code":"input double LotSize = 0.50;"}]}`

// TriageSchema returns the structured-output JSON schema for the triage pass.
func TriageSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chunk_id":        map[string]any{"type": "string"},
			"has_code_action": map[string]any{"type": "boolean"},
			"confidence":      map[string]any{"type": "number"},
		},
		"required": []string{"chunk_id", "has_code_action", "confidence"},
	}
}

// DeepSchema returns the structured-output JSON schema for the deep pass.
func DeepSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"events": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"chunk_id": map[string]any{"type": "string"},
						"seq":      map[string]any{"type": "integer"},
						"op":       map[string]any{"type": "string", "enum": []string{"create", "append", "replace", "property", "include"}},
						"file":     map[string]any{"type": "string"},
						"anchor":   map[string]any{"type": "string"},
						"code":     map[string]any{"type": "string"},
					},
					"required": []string{"chunk_id", "seq", "op", "file", "code"},
				},
			},
		},
		"required": []string{"events"},
	}
}

// TriageUser builds the user message for one chunk. Only the tail varies;
// everything before the chunk body stays byte-identical across calls.
func TriageUser(chunkID, text string) string {
	const head = "Classify this chunk.\n\nChunk "
	return head + chunkID + ": " + text
}

// DeepUser builds the user message for one chunk in the deep pass.
func DeepUser(chunkID, text string) string {
	const head = "Extract code events from this chunk.\n\nChunk "
	return head + chunkID + ": " + text
}
