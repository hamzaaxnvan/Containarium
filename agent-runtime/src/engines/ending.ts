// How an engine's turn ended, rendered as the journal's status row (#2513).
// The Gemini engine writes the same row from its own finish reason
// (describeGeminiEnding); this is the shape the other engines share, so a
// journal reader greps one prefix whichever engine ran.

// FINAL_MESSAGE_PREVIEW_CHARS bounds the final-message preview on the row.
// The full text is already its own assistant row; the preview only puts the
// start of it beside the stop reason.
export const FINAL_MESSAGE_PREVIEW_CHARS = 200;

