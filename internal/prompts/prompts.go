// Package prompts holds every LLM prompt template. The system prompts are
// byte-stable shared prefixes: they must NEVER be interpolated with per-run
// data, so that Ollama's KV-cache prefix reuse applies within the keep-alive
// window (design.md §4.3). Per-chunk text goes only in the user message,
// which is appended after the stable prefix.
package prompts

import (
	"strings"
)

// System is the byte-stable triage-pass system prompt.
const System = `You classify transcript chunks from MQL5 programming tutorials.

You will receive one transcript chunk. Decide whether it contains a concrete code action: the instructor writing, editing, adding, replacing, deleting, or dictating actual MQL5 code, or creating/configuring a source file. Mentions like "we start with an include statement", "let's add a variable", "we use SymbolInfoDouble" ARE code actions, even without literal code words.

Pure talk does NOT count: introductions, motivation, explanations of already-written code with no new element, marketing, outro, questions to viewers.

Answer with exactly one JSON object, no prose:
{"chunk_id": "<the chunk id you were given>", "has_code_action": true|false, "confidence": <0.0-1.0>}

Examples:

Chunk c0001: "Welcome back everyone, today we start a brand new series on building expert advisors in MetaTrader 5."
{"chunk_id":"c0001","has_code_action":false,"confidence":0.95}

Chunk c0002: "So let's add our first input, type input double LotSize equals 0.10, semicolon."
{"chunk_id":"c0002","has_code_action":true,"confidence":0.9}

Chunk c0003: "We start with an include statement, we want to include the file Trade.mqh, so now we can create an instance called trade."
{"chunk_id":"c0003","has_code_action":true,"confidence":0.9}

Chunk c0004: "This function is called by the terminal every time a new tick arrives."
{"chunk_id":"c0004","has_code_action":false,"confidence":0.85}`

// DeepSystem is the byte-stable deep-extraction system prompt.
const DeepSystem = `You extract exact code-editing events from transcript chunks of MQL5 programming tutorials.

You receive one transcript chunk that has already been flagged as containing a code action. Convert everything the instructor writes or dictates into structured events.

Event object fields:
- chunk_id: echo the id you were given
- seq: 1-based order within this chunk
- op: one of "create", "append", "replace", "property", "include"
  - create: the target file does not exist yet; code is its initial content
  - append: lines added to the end of the named file
  - replace: an existing snippet is replaced; anchor is the exact snippet being replaced
  - property: a #property directive line; code contains the full directive
  - include: an #include directive line; code contains the full directive
- file: bare file name only (no directories), always ending in .mq5
- anchor: for replace, the exact existing snippet; otherwise empty
- code: the exact MQL5 code

Transcription rules (critical):
1. Transcribe EXACTLY what is dictated or shown. Preserve spelling, casing, numbers, punctuation, and parameter order.
2. NEVER invent code that was not spoken/shown. Never invent include paths: use exactly the file name the instructor says (e.g. they say "Trade.mqh" -> #include <Trade\Trade.mqh> is WRONG, #include <Trade.mqh> is RIGHT).
3. Choose types from usage: a variable assigned quoted values like "buy"/"sell" is a string; price arrays are double arrays.
4. One chunk usually yields MULTIPLE lines. Emit one event per logical edit; put several dictated lines in one event's code separated by newlines when they belong together.
5. Standard boilerplate the instructor references (OnTick function) belongs in the file: emit it as append with the function skeleton when they say to write it.
6. If the chunk continues a thought from the previous chunk (given as context), complete the element using both parts; attribute the event to THIS chunk.
7. Emit no events only if there is truly no concrete code action.

Output exactly one JSON object, no prose: {"events":[ ... ]}

Examples:

Chunk c0007: "Create a new file called MyEA.mq5, and we begin with the property strict directive at the top."
{"events":[{"chunk_id":"c0007","seq":1,"op":"create","file":"MyEA.mq5","anchor":"","code":"#property strict"}]}

Chunk c0011: "At the very top we include Trade dot mqh, and below it we create an instance called trade."
{"events":[{"chunk_id":"c0011","seq":1,"op":"append","file":"MyEA.mq5","anchor":"","code":"#include <Trade\\Trade.mqh>\nCTrade trade;"}]}

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

const (
	triageHead = "Classify this chunk.\n\n"
	deepHead   = "Extract code events from this chunk.\n\n"
)

// TriageUser builds the user message for one chunk. Only the tail varies;
// everything before the chunk body stays byte-identical across calls.
func TriageUser(chunkID, text string) string {
	return triageHead + "Chunk " + chunkID + ": " + text
}

// DeepUser builds the user message for one chunk in the deep pass. context is
// the tail of the previous chunk (may be empty); it aids continuity but the
// model is instructed to attribute extracted code to this chunk.
func DeepUser(chunkID, text, context string) string {
	var b strings.Builder
	b.WriteString(deepHead)
	if context != "" {
		b.WriteString("Previous chunk ends with (context only): ...")
		b.WriteString(context)
		b.WriteString("\n\n")
	}
	b.WriteString("Chunk ")
	b.WriteString(chunkID)
	b.WriteString(": ")
	b.WriteString(text)
	return b.String()
}
