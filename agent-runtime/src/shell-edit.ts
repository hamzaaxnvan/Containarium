// Detecting a shell command that writes files (#3107 item 1).
//
// A run that edits only through shell_exec (agent-box) or Codex's native shell
// would otherwise get no re-prompt for a missing final message. A shell
// command is free text, so this is a heuristic, and it is built to be
// conservative: it names only the unmistakable write forms, so a run that only
// reads is not told it "changed files".
//
// Counted: an output redirection (> >> >|) to anything but /dev/*, tee, sed -i
// / perl -i, rm, mv, cp, touch, install, truncate, patch, git apply|mv|rm.
// Not counted: reads, searches, builds and test runs, package installs, mkdir
// and ln, and anything that writes through a program we cannot see into
// (python script.py, make, npm run build). Those runs fall back to the old
// behaviour, a failure with the reason in the journal. Text inside quotes is
// ignored, so `grep '>' f` and `echo "a > b"` are not writes.
//
// Known limits (#3107 review). Misses: git checkout/restore/reset/clean,
// xargs rm, find -delete / -exec rm, dd of=, a bash -c nested inside bash -c,
// backtick and $( ) substitutions, and any interpreter that writes from its own
// code (python, node, make). False positives: a backslash-escaped `>`,
// `$((1>2))` and `[[ 1 > 2 ]]` read as redirections, an apostrophe in
// unquoted text (`echo don't "a > b"`) opens a quote that never closes so the
// rest is read as code, and here-document bodies
// are scanned as if they were commands. A command over MAX_SHELL_COMMAND_LENGTH
