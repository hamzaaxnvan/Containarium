import { describe, expect, it } from "vitest";
import { sumUsage } from "./usage.js";

describe("sumUsage (#3107 item 2)", () => {
  it("adds numeric fields, including nested objects", () => {
    expect(
      sumUsage(
        { input_tokens: 10, output_tokens: 3, cache: { read: 4 } },
        { input_tokens: 5, output_tokens: 2, cache: { read: 1, write: 7 } },
      ),
