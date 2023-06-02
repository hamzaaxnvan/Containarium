import type { GenerateContentResponse } from "@google/genai";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { EngineConfig } from "../engine.js";
import { CaptureJournal } from "../testing/capture.js";
import { GeminiEngine, geminiFollowUp, journalGeminiResponse } from "./gemini.js";

// The Gemini engine makes one generateContent call with automatic function
// calling; the SDK returns the tool-use turns as automaticFunctionCallingHistory
// alongside the final text. That history is the engine's event stream.
function fakeResponse(r: { history: unknown[]; text: string }): Pick<GenerateContentResponse, "automaticFunctionCallingHistory" | "text"> {
  return { automaticFunctionCallingHistory: r.history as GenerateContentResponse["automaticFunctionCallingHistory"], text: r.text };
}

describe("GeminiEngine journal", () => {
  it("journals assistant, tool_use and tool_result one line each, in order", () => {
    const j = new CaptureJournal();
    journalGeminiResponse(
      fakeResponse({
        history: [
          { role: "user", parts: [{ text: "the task" }] },
          { role: "model", parts: [{ text: "Looking." }, { functionCall: { name: "shell", args: { cmd: "ls" } } }] },
          { role: "user", parts: [{ functionResponse: { name: "shell", response: { content: [{ type: "text", text: "README.md" }] } } }] },
          { role: "model", parts: [{ text: "hmm", thought: true }] },
        ],
        text: '{"files":1}',
      }),
      j,
    );
    expect(j.entries).toEqual([
      { kind: "assistant", text: "Looking." },
      { kind: "tool_use", tool: "shell", input: '{"cmd":"ls"}' },
      { kind: "tool_result", tool: "shell", text: '{"content":[{"type":"text","text":"README.md"}]}' },
      { kind: "assistant", text: '{"files":1}' },
    ]);
  });

  it("journals only the missing final message when there was no tool use and no text", () => {
    const j = new CaptureJournal();
    journalGeminiResponse(fakeResponse({ history: [], text: "" }), j);
    expect(j.entries).toEqual([{ kind: "status", text: "model ended without a final message: finish_reason=unknown" }]);
  });

  // #2513: a run that edited files and then stopped with no final JSON left no
  // trace of why. The journal now says how the turn ended.
  describe("how the turn ended (#2513)", () => {
    const withEnding = (text: string, extra: Record<string, unknown>) =>
      ({ ...fakeResponse({ history: [], text }), ...extra }) as Parameters<typeof journalGeminiResponse>[0];

    it("names a STOP with no final text", () => {
      const j = new CaptureJournal();
      journalGeminiResponse(withEnding("", { candidates: [{ finishReason: "STOP" }] }), j);
      expect(j.entries).toEqual([{ kind: "status", text: "model ended without a final message: finish_reason=STOP" }]);
    });

    it("names MAX_TOKENS, the finish message and a prompt block", () => {
      const j = new CaptureJournal();
      journalGeminiResponse(
        withEnding("", { candidates: [{ finishReason: "MAX_TOKENS", finishMessage: "limit hit" }], promptFeedback: { blockReason: "SAFETY" } }),
        j,
      );
      expect(j.entries).toEqual([
        { kind: "status", text: 'model ended without a final message: finish_reason=MAX_TOKENS finish_message="limit hit" block_reason=SAFETY' },
      ]);
    });

    it("stays quiet for a normal STOP with a final message", () => {
      const j = new CaptureJournal();
      journalGeminiResponse(withEnding('{"ok":1}', { candidates: [{ finishReason: "STOP" }] }), j);
      expect(j.entries).toEqual([{ kind: "assistant", text: '{"ok":1}' }]);
    });

    it("flags a final message cut off by MAX_TOKENS", () => {
      const j = new CaptureJournal();
      journalGeminiResponse(withEnding('{"ok":', { candidates: [{ finishReason: "MAX_TOKENS" }] }), j);
      expect(j.entries).toEqual([
        { kind: "assistant", text: '{"ok":' },
        { kind: "status", text: "model ended abnormally after its final message: finish_reason=MAX_TOKENS" },
      ]);
    });

    it("bounds the finish message", () => {
      const j = new CaptureJournal();
      journalGeminiResponse(withEnding("", { candidates: [{ finishReason: "OTHER", finishMessage: "x".repeat(1000) }] }), j);
