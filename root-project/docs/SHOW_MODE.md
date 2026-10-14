# Show Mode (third Browser row) — manual login, then live chat in the browser

Available options: 01 Rod, 02 ChromeDP, 03 Show Mode. Each has Run.

## Behaviour (DOM edition, 2026-10-14)

Show Mode is a visible Rod preset with `live_chat` enabled. Everything after
login happens **through the visible page only** — message rows are read from
the DOM, replies are typed into the message box, and Skip/START are real
button clicks. No WebSocket frames and no chat API calls are used (explicit
user requirement; the earlier transport-based edition was retired).

1. Run opens a **visible** Rod browser at `app.chitchat.gg` (Headless is
   ignored for this row). Login itself is unchanged: cookies/storage capture
   runs as before, and the login JSON is saved exactly like the other rows.
2. **You** enter email/password and solve any captcha manually. The bot
   never touches credentials or captcha.
3. Once login is detected and saved, the browser **does not close**. The
   live runner watches the page:
   - **Lobby:** if a START button is visible it is clicked (throttled);
     if no chat box is found, a one-line page probe (url / inputs / buttons
     / message rows) is logged so a layout problem is visible immediately.
   - **Chatting:** rows already on screen are seeded as history (never
     answered). New partner rows are detected by author+text copy counts;
     own echoes (the text just typed, re-rendered) are suppressed and never
     answered twice. System rows ("left the chat", …) are ignored.
   - **Replies** come from the currently selected dashboard message mode:
     EVAChat Flow uses the exported EVA engine (Start/Reply per
     conversation); Single/Folder modes pick ONE sequence per conversation
     (Folder picks randomly among the loaded files, like the worker bot)
     and walk its lines, greeting first. If the reply engine fails to load,
     messages are still detected and logged but not answered.
   - **Sending** types into the "Send a message..." box (execCommand, with
     a native-setter fallback for React-style inputs), clicks the send
     button when found (otherwise Enter), then **verifies** the text
     actually appears in the chat before logging `SEND`.
   - **Rotation:** when a flow ends, Skip (or End/START) is clicked and a
     fresh conversation begins with a new greeting. "Chat ended" / partner
     left returns the runner to the lobby, where START is clicked again.
   - Live events are logged at ACTIVITY level so they appear in the Home
     log stream: `INCOMING | partner: "text"`, `SEND | reply: "text"`,
     `SKIP`, `START clicked`, `PARTNER LEFT`.
4. **Stop:** close the browser window (live chat ends within a few seconds)
   or close the app. The usual temporary-profile cleanup then runs.

Detection heuristics (`internal/browser/domchat.go`) are structural because
the site uses utility CSS classes without semantic message selectors:
message rows are rendered elements containing a clock time (`10:24 PM`),
whose first line is the author line and whose remaining lines are the
message (a "name: text" single-line layout is also accepted). The message
box is found by placeholder/aria-label "message" first, else the last
visible text input. Buttons are matched by visible label text. These
heuristics were unit-tested against a simulated page layout with jsdom,
but the real site layout is only proven by a Windows run — read the page
probe line in the log if anything looks off.

## Settings and persistence

The frontend sends `show_mode`; `validateBrowserRun` normalizes it to
`backend=rod`, `headless=false`, `live_chat=true` **before** saving defaults
or dispatching, so saved sessions keep the real backend name. Rod/ChromeDP
rows never set `live_chat`. The Headless checkbox is not changed by the
click; Rod/ChromeDP continue to use it normally.

## Tests (browser-free)

- `live_chat_test.go`: fake DOM backend drives the whole runner — START
  click, greeting, partner reply, own-echo suppression, skip + re-match,
  partner-left recovery, one-sequence-per-conversation, EVA start.
- The DOM JavaScript heuristics were exercised in Node with jsdom against
  a simulated chat layout (five row layouts, input discovery, partner
  header, button scan, typing fallback): all checks passed.
- Full Go compilation and a real Windows browser run remain subject to the
  verification report; no live browser was run while writing this.
