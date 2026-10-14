# Project Memory — decisions and current state

Purpose: when this project is shared with another agent/model, this file plus
`AGENTS.md` and `docs/PROJECT_MAP.md` should be enough to understand where
things stand without re-asking the user. The AUTO block below is refreshed by
`scripts/project-map.cjs`; the decision log is manual — **append an entry
after every meaningful change** (newest last). Never rewrite old entries.

<!-- AUTO:BEGIN -->
_Auto status 2026-10-14. Do not edit this block by hand._

- Source fingerprint: `2ca4d3d2e0e17bc8…`
- Browser options: `rod`, `chromedp`, `show_mode`
- Capture tool: v1.2.1
- Last verification: **BLOCKED** (PASS 3, FAIL 0, BLOCKED 10) — BLOCKED is not PASS; see `verification-output/report.json`
<!-- AUTO:END -->

## Decision log

- (template) YYYY-MM-DD — what changed, why, and how it was verified.
- 2026-10-14 — Browser direct-run: numbered Rod/ChromeDP rows each with their
  own Run button; shared Profile/Count (1–5)/Headless; accepted settings are
  remembered. Browser-free verification system added (see docs/VERIFICATION.md).
- 2026-10-14 — Show Mode added as Browser option 03: a visible Rod preset.
  Backend normalizes `show_mode` → `rod` + `headless=false` before saving or
  dispatching, so saved sessions stay compatible. Not an anti-detection mode.
- 2026-10-14 — Standalone manual capture tool added under `capture/`
  (dependency-free Node + the user's Chrome/Edge via CDP). Captures HTTP,
  WebSocket frames, DOM message hints and storage, redacted by default, into
  `capture/capture-sessions/` (excluded from source packages). Purpose: the
  user captures real traffic manually so future WebSocket/API automation can
  be built from evidence instead of guesses.
- 2026-10-14 — Project map/memory system added: this file plus
  docs/PROJECT_MAP.md are auto-refreshed by scripts/project-map.cjs on every
  `npm run verify`. Capture tool gained a WebSocket event catalog, manifest
  schema fingerprints and a session compare tool (`capture/tool/compare.cjs`)
  so a future chitchat.gg API/WebSocket change can be diagnosed by capturing
  again and diffing against a baseline instead of starting from zero.
- 2026-10-14 — Capture tool v1.2.0 after first real Windows run failed with a
  bare "fetch failed" (DevTools endpoint was polled only once). Fixes: free
  port selection + 45s DevTools polling with browser-stderr `browser.log`,
  `tool/doctor.cjs` smoke test (actually launches the browser via CDP),
  one-click `install.ps1` (winget → MSI fallback for Node, winget Chrome
  fallback), live capability checklist + `[live]` status lines in the
  terminal, and a session `SUMMARY.md`. User machine: Node 24 + Microsoft
  Edge confirmed by the old install check.
- 2026-10-14 — First real ChitChat capture analysed end-to-end and distilled
  into `docs/CHITCHAT_PROTOCOL.md` + `docs/chitchat-protocol.json` (baseline
  session 20261014-185017, schema fingerprints recorded there). Proven facts:
  Socket.IO `chatMessage` = incoming; sending = `POST
  /users/me/conversations/:id/messages` multipart `{content, nonce}`;
  skip = `PATCH /match/disconnect` + `POST /match`; session is cookie-based.
  Existing `internal/bot/realtime.go` speaks the same socket protocol, so the
  capture confirms it. Show Mode live-chat design (in-browser, CDP frames +
  page-context fetch, no DOM selectors) is documented in the protocol file
  and awaits user approval. Capture redactor no longer hides `author` fields
  (v1.2.1).
- 2026-10-14 — Show Mode live chat implemented (user-approved, evidence from
  docs/CHITCHAT_PROTOCOL.md): `BrowserSettings.LiveChat` is set only by the
  Show Mode preset; after login save, `app.runLiveChat` keeps the Rod browser
  open and chats in place — incoming from CDP Socket.IO `chatMessage` frames
  (`internal/browser` `ChatMessageSource`, Rod-only), replies from the
  selected mode (EVA engine via exported Start/Reply; sequences walk their
  lines; EVA AI compose not applied live), sent via the page's own
  authenticated fetch (multipart `content`+`nonce`), skip via
  `PATCH /match/disconnect` + `POST /match`. Closing the browser stops it.
  Browser-free fake-backend tests cover greeting/reply/own-ignore/dedupe/
  skip; full Go compile and live Windows run still pending user testing.
- 2026-10-14 — Show Mode live fix after the user's first real test (Folder
  Sequences sent one line, incoming never detected): ROOT CAUSE — the real
  `chatMessage` author is an object `{id, username}` (as the existing
  realtime.go parses), but the new live parser declared it a string, so
  every real frame failed to parse. Parser now accepts object/string/null
  authors (isolated tests pass with the real shape). Also: API
  history-polling fallback for detection, history seeding so old messages
  are never answered, Folder mode picks one sequence per conversation
  (not all files flattened), transient-empty partner-left tolerance, and
  live INCOMING/SEND/SKIP logs at ACTIVITY level so they show on Home.
- 2026-10-14 — Show Mode rebuilt DOM-only (user explicitly retired the
  WebSocket/API approach: "only browser diye, no websocket api"): the old
  `internal/browser/livechat*.go` transport was deleted; Show Mode live chat
  now reads message rows from the page DOM, types replies into the message
  box, and clicks Skip/START (`internal/browser/domchat.go` +
  `live_chat.go`). Structural heuristics: rows are elements containing a
  `10:24 PM`-style time whose first line is the author; input found by
  "message" placeholder; buttons matched by label. Runner seeds history,
  dedupes by author+text counts, suppresses own echoes, clicks START in the
  lobby, logs a one-line page probe when no chat box is visible. Verified:
  full Windows build (`GOOS=windows go build ./...`) PASSED in a temp copy
  using GitHub-mirror replaces for the blocked vanity module paths (real
  go.mod untouched); Go tests compile and `go vet` is clean; the runner
  state machine was EXECUTED against a fake DOM backend in a throwaway
  Linux harness (START→greeting→reply→echo-suppression→skip→re-match→
  partner-left; it caught and fixed a 6s START-throttle that delayed
  re-matching); jsdom simulation of the DOM heuristics 12/12 PASS; Node
  source tests PASS. The protocol docs remain as evidence only; real DOM
  behaviour on the live site awaits the user's Windows retest. Options
  01/02 untouched.
- 2026-10-14 — HANDOFF / session close (user request): DOM Show Mode
  packaged as `root-project-showmode-dom-UNVERIFIED.zip` (repo root;
  integrity: `node scripts/package-source.cjs --check <zip>`). Full
  `root-project/` source is committed to git from this push on (it was
  previously delivered as zip-only). **Next session start:** read
  AGENTS.md + docs/PROJECT_MAP.md + this file + docs/SHOW_MODE.md.
  **Awaiting:** user's Windows retest of the DOM build. If detection fails,
  the Home log's `Page probe:` line shows what the bot saw — fix the
  heuristics in `internal/browser/domchat.go` from that evidence; do not
  revive the WebSocket/API route for Show Mode.

## Known limitations (keep honest)

- Full Go/Windows verification depends on module downloads and a Windows
  environment; sandbox runs may legitimately end BLOCKED. BLOCKED is not PASS.
- Live browser/login behaviour is only proven by the user's manual Windows
  test unless explicitly stated otherwise.

## How to add memory

1. Run your change and its tests.
2. Run `npm run map:update` (or `npm run verify`, which does it first).
3. Append one dated bullet to the decision log above: change + reason +
   verification evidence. Keep secrets, tokens and account data out.
