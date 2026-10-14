package main

// Show Mode live chat, DOM-only edition. After the user logs in manually in
// the visible Rod browser, chatting continues inside that same browser using
// only what a human sees: message rows are read from the DOM, replies are
// typed into the message box, and Skip/START are real button clicks. No
// WebSocket frames and no API calls are used (explicit user requirement).
// Normal Rod/ChromeDP login capture and the Start-Bot worker are untouched;
// this runs only when BrowserSettings.LiveChat is set (Show Mode).

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"chitchatbot/internal/bot"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

// ---------------------------------------------------------------------------
// Reply responders (unchanged semantics). EVA mode reuses the exported
// EVAChat engine; Folder mode picks ONE sequence per conversation, like the
// worker bot; Single mode walks its lines.
// ---------------------------------------------------------------------------

type liveResponder interface {
	Start() []string
	Reply(incoming string) (lines []string, end bool)
}

type detectOnlyResponder struct{}

func (detectOnlyResponder) Start() []string { return nil }
func (detectOnlyResponder) Reply(string) ([]string, bool) {
	return nil, false
}

type sequenceLiveResponder struct {
	sequences [][]string
	lines     []string
	next      int
	rng       *rand.Rand
}

func (r *sequenceLiveResponder) Start() []string {
	usable := r.sequences[:0]
	for _, seq := range r.sequences {
		if len(seq) > 0 {
			usable = append(usable, seq)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	pick := 0
	if len(usable) > 1 && r.rng != nil {
		pick = r.rng.Intn(len(usable))
	}
	r.lines = usable[pick]
	r.next = 1
	return []string{r.lines[0]}
}

func (r *sequenceLiveResponder) Reply(string) ([]string, bool) {
	if r.next < len(r.lines) {
		line := r.lines[r.next]
		r.next++
		return []string{line}, false
	}
	return nil, true
}

type evaLiveResponder struct {
	engine *bot.EVAChatEngine
	state  *model.EVAChatSession
	rng    *rand.Rand
}

func evaLines(result bot.EVAReplyResult) []string {
	lines := []string{}
	if strings.TrimSpace(result.Text) != "" {
		lines = append(lines, result.Text)
	}
	for _, extra := range result.Extra {
		if strings.TrimSpace(extra) != "" {
			lines = append(lines, extra)
		}
	}
	return lines
}

func (r *evaLiveResponder) Start() []string {
	return evaLines(r.engine.Start(r.state, r.rng))
}

func (r *evaLiveResponder) Reply(incoming string) ([]string, bool) {
	result := r.engine.Reply(incoming, r.state, r.rng)
	return evaLines(result), result.Ended
}

func liveSequences(settings model.MessageSettings) [][]string {
	if settings.EffectiveChatMode() == model.ChatModeFolder && len(settings.LoadedSequences) > 0 {
		sequences := make([][]string, 0, len(settings.LoadedSequences))
		for _, seq := range settings.LoadedSequences {
			sequences = append(sequences, model.MessageSequenceLines(seq))
		}
		return sequences
	}
	return [][]string{settings.SequenceLines()}
}

func (a *App) newLiveResponder() (liveResponder, string, error) {
	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, "", err
	}
	mode := settings.EffectiveChatMode()
	if mode == model.ChatModeEVAChat {
		dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
		if err != nil {
			return nil, "", err
		}
		engine, err := bot.LoadEVAChatEngine(dataRoot)
		if err != nil {
			return nil, "", err
		}
		session := bot.NewEVAChatSession()
		return &evaLiveResponder{
			engine: engine,
			state:  &session,
			rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
		}, "EVAChat Flow", nil
	}
	return &sequenceLiveResponder{
		sequences: liveSequences(settings),
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}, mode, nil
}

// ---------------------------------------------------------------------------
// DOM runner: state machine over the visible page.
//   no input  -> lobby: click START when present, otherwise keep watching
//   input     -> chatting: seed rows, greet, answer new partner rows,
//                skip at flow end; "chat ended" returns to lobby
// ---------------------------------------------------------------------------

type liveRunner struct {
	dom          browser.DOMChatBackend
	prefix       string
	logf         func(level, message string)
	newResponder func() (liveResponder, string, error)
	replyDelay   time.Duration
	pollInterval time.Duration

	convActive    bool
	responder     liveResponder
	seenCounts    map[string]int
	sentEcho      map[string]int
	echoAuthors   map[string]int
	ownName       string
	partnerName   string
	lastStartAt   time.Time
	probeLogged   bool
	lastStepError string
}

func (a *App) runLiveChat(ctx context.Context, backend browser.Backend, login model.LoginInfo, profileName string, index int, total int) {
	prefix := fmt.Sprintf("[%s show-live %d/%d]", profileName, index, total)
	dom, ok := backend.(browser.DOMChatBackend)
	if !ok {
		a.log("error", fmt.Sprintf("%s Live chat needs the Rod backend; the %s backend cannot drive the page DOM, so the browser will close as usual.", prefix, backend.Name()))
		return
	}
	r := &liveRunner{
		dom:          dom,
		prefix:       prefix,
		logf:         a.log,
		replyDelay:   900 * time.Millisecond,
		pollInterval: 1500 * time.Millisecond,
		seenCounts:   make(map[string]int),
		sentEcho:     make(map[string]int),
		echoAuthors:  make(map[string]int),
		newResponder: a.newLiveResponder,
	}
	a.log("activity", fmt.Sprintf("%s LIVE CHAT started (browser DOM mode). Login/match manually if needed; close the browser window to stop.", prefix))
	r.run(ctx)
	a.log("activity", fmt.Sprintf("%s LIVE CHAT stopped.", prefix))
}

func (r *liveRunner) run(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if r.step(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step performs one watch cycle. Returns true when the runner must stop.
func (r *liveRunner) step(ctx context.Context) bool {
	controls, err := r.dom.DOMControls(ctx)
	if err != nil {
		if r.pageGone(err) {
			return true
		}
		r.logStepError(fmt.Sprintf("page state check failed: %v", err))
		return false
	}
	if controls.Partner != "" {
		r.partnerName = controls.Partner
	}

	if controls.ChatEnded && r.convActive {
		r.logf("activity", fmt.Sprintf("%s PARTNER LEFT | chat ended on page; looking for a new match.", r.prefix))
		r.convActive = false
		r.responder = nil
		r.lastStartAt = time.Time{}
	}

	if !controls.HasInput {
		if r.convActive {
			r.convActive = false
			r.responder = nil
			r.logf("info", fmt.Sprintf("%s Chat input disappeared; back to watching for a new chat.", r.prefix))
		}
		if hasDOMButton(controls.Buttons, "START") {
			if time.Since(r.lastStartAt) > 6*time.Second {
				r.lastStartAt = time.Now()
				clicked, err := r.dom.ClickDOMButton(ctx, []string{"START"})
				if err != nil {
					if r.pageGone(err) {
						return true
					}
					r.logStepError(fmt.Sprintf("START click failed: %v", err))
				} else if clicked != "" {
					r.logf("activity", fmt.Sprintf("%s START clicked | matching with a new partner...", r.prefix))
				}
			}
		} else if !r.probeLogged {
			r.probeLogged = true
			probe, perr := r.dom.DOMProbe(ctx)
			if perr == nil {
				r.logf("info", fmt.Sprintf("%s Page probe: %s", r.prefix, probe))
			}
			r.logf("info", fmt.Sprintf("%s Chat box not visible yet. If the page is waiting, click START/match manually once; the bot keeps watching.", r.prefix))
		}
		return false
	}

	// Chat ended but the (disabled) input is still rendered: restart via
	// START instead of greeting a dead conversation.
	if controls.ChatEnded {
		if hasDOMButton(controls.Buttons, "START") && time.Since(r.lastStartAt) > 6*time.Second {
			r.lastStartAt = time.Now()
			if _, err := r.dom.ClickDOMButton(ctx, []string{"START"}); err != nil && r.pageGone(err) {
				return true
			}
		}
		return false
	}

	// Chat input visible.
	if !r.convActive {
		return r.beginConversation(ctx)
	}

	rows, err := r.dom.ExtractDOMMessages(ctx)
	if err != nil {
		if r.pageGone(err) {
			return true
		}
		r.logStepError(fmt.Sprintf("message read failed: %v", err))
		return false
	}
	for _, m := range r.newRows(rows) {
		if r.handlePartnerMessage(ctx, m) {
			return true
		}
		if !r.convActive {
			break // skipped mid-batch
		}
	}
	return false
}

func (r *liveRunner) beginConversation(ctx context.Context) bool {
	if rows, err := r.dom.ExtractDOMMessages(ctx); err == nil {
		r.seedRows(rows)
	} else if r.pageGone(err) {
		return true
	}
	responder, mode, err := r.newResponder()
	if err != nil {
		r.logf("error", fmt.Sprintf("%s Reply engine unavailable (%v); incoming will be shown but not answered.", r.prefix, err))
		responder = detectOnlyResponder{}
		mode = "detect-only"
	}
	r.responder = responder
	r.convActive = true
	r.logf("activity", fmt.Sprintf("%s CHAT STARTED | partner: %s | reply mode: %s", r.prefix, orUnknown(r.partnerName), mode))
	return r.sendLines(ctx, responder.Start())
}

// newRows returns rows that appeared since the last snapshot (by
// author+text counts, so repeated identical texts are handled per copy).
func (r *liveRunner) newRows(rows []browser.DOMMessage) []browser.DOMMessage {
	current := make(map[string]int)
	for _, row := range rows {
		current[domRowKey(row)]++
	}
	consumed := make(map[string]int)
	var out []browser.DOMMessage
	for _, row := range rows {
		k := domRowKey(row)
		consumed[k]++
		if consumed[k] > r.seenCounts[k] {
			out = append(out, row)
		}
	}
	for k, v := range current {
		if v > r.seenCounts[k] {
			r.seenCounts[k] = v
		}
	}
	return out
}

func (r *liveRunner) seedRows(rows []browser.DOMMessage) {
	// Fresh baseline for this conversation: anything already rendered is
	// history and must never be answered.
	r.seenCounts = make(map[string]int)
	for _, row := range rows {
		r.seenCounts[domRowKey(row)]++
	}
}

func domRowKey(m browser.DOMMessage) string {
	return m.Author + "\x00" + m.Text
}

func (r *liveRunner) handlePartnerMessage(ctx context.Context, m browser.DOMMessage) bool {
	text := strings.TrimSpace(m.Text)
	if text == "" || isLiveSystemRow(m) {
		return false
	}
	// Own echo: the page re-renders what we typed. Match by exact text that
	// we recently sent and learn our display name from the row.
	if r.sentEcho[text] > 0 && (r.partnerName == "" || m.Author != r.partnerName) {
		r.sentEcho[text]--
		// Learn our own display name only from repeated echoes: a partner
		// who happens to send the same text once must never be muted.
		if m.Author != "" {
			r.echoAuthors[m.Author]++
			if r.echoAuthors[m.Author] >= 2 {
				r.ownName = m.Author
			}
		}
		return false
	}
	if r.ownName != "" && m.Author == r.ownName {
		return false
	}
	if r.partnerName == "" && m.Author != "" {
		r.partnerName = m.Author
	}

	r.logf("activity", fmt.Sprintf("%s INCOMING | %s: %q", r.prefix, orUnknown(m.Author), truncateLive(text, 120)))
	if sleepLive(ctx, r.replyDelay) {
		return true
	}
	lines, end := r.responder.Reply(text)
	if r.sendLines(ctx, lines) {
		return true
	}
	if end {
		r.logf("activity", fmt.Sprintf("%s SKIP | flow finished; clicking Skip/START for a new partner.", r.prefix))
		clicked, err := r.dom.ClickDOMButton(ctx, []string{"Skip", "End Chat", "End", "START", "New Chat", "Start"})
		if err != nil {
			if r.pageGone(err) {
				return true
			}
			r.logf("error", fmt.Sprintf("%s Skip click failed: %v", r.prefix, err))
		} else if clicked == "" {
			r.logf("error", fmt.Sprintf("%s Skip requested but no Skip/START button was found on the page. Click it manually once; the bot keeps watching.", r.prefix))
		} else {
			r.logf("activity", fmt.Sprintf("%s Clicked %q for the next partner.", r.prefix, clicked))
		}
		r.convActive = false
		r.responder = nil
		r.lastStartAt = time.Time{} // match again without the lobby throttle
	}
	return false
}

func (r *liveRunner) sendLines(ctx context.Context, lines []string) bool {
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i > 0 && sleepLive(ctx, 400*time.Millisecond) {
			return true
		}
		sent, err := r.dom.SendDOMMessage(ctx, line)
		if err != nil {
			if r.pageGone(err) {
				return true
			}
			r.logf("error", fmt.Sprintf("%s Could not type reply into the page: %v", r.prefix, err))
			return false
		}
		if !sent {
			r.logf("error", fmt.Sprintf("%s Reply %q was typed but did not appear in the chat; check the page (send button/Enter behaviour).", r.prefix, truncateLive(line, 80)))
			return false
		}
		r.sentEcho[line]++
		r.logf("activity", fmt.Sprintf("%s SEND | reply: %q", r.prefix, truncateLive(line, 120)))
	}
	return false
}

func (r *liveRunner) pageGone(err error) bool {
	if errors.Is(err, browser.ErrPageUnavailable) {
		r.logf("activity", fmt.Sprintf("%s Browser page closed; live chat ending.", r.prefix))
		return true
	}
	return false
}

func (r *liveRunner) logStepError(msg string) {
	if msg == r.lastStepError {
		return // do not spam the same failure every cycle
	}
	r.lastStepError = msg
	r.logf("error", fmt.Sprintf("%s %s", r.prefix, msg))
}

func hasDOMButton(buttons []string, label string) bool {
	for _, b := range buttons {
		if strings.EqualFold(strings.TrimSpace(b), label) {
			return true
		}
	}
	for _, b := range buttons {
		if strings.Contains(strings.ToLower(b), strings.ToLower(label)) {
			return true
		}
	}
	return false
}

func isLiveSystemRow(m browser.DOMMessage) bool {
	text := strings.ToLower(m.Text + " " + m.Author)
	for _, marker := range []string{
		"left the chat", "match found", "you are now chatting", "premium unlocks",
		"messages like this can be hidden", "chat ended", "safety settings",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "partner"
	}
	return s
}

func truncateLive(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func sleepLive(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
