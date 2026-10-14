package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

// fakeDOMBackend implements browser.Backend + browser.DOMChatBackend with a
// scripted page. No browser is launched: rows and controls are faked, typing
// is recorded, and sends echo back as own rows just like the real page.
type fakeDOMBackend struct {
	mu       sync.Mutex
	controls browser.DOMControls
	rows     []browser.DOMMessage
	sent     []string
	clicks   []string
}

func (f *fakeDOMBackend) Name() string { return "rod" }
func (f *fakeDOMBackend) Start(context.Context, browser.Options) error {
	return nil
}
func (f *fakeDOMBackend) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{}, nil
}
func (f *fakeDOMBackend) Fetch(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
	return browser.FetchResponse{}, nil
}
func (f *fakeDOMBackend) Close() error { return nil }

func (f *fakeDOMBackend) DOMControls(context.Context) (browser.DOMControls, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.controls, nil
}

func (f *fakeDOMBackend) ExtractDOMMessages(context.Context) ([]browser.DOMMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]browser.DOMMessage(nil), f.rows...), nil
}

func (f *fakeDOMBackend) SendDOMMessage(_ context.Context, text string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	f.rows = append(f.rows, browser.DOMMessage{Author: "me", Text: text})
	return true, nil
}

func (f *fakeDOMBackend) DOMProbe(context.Context) (string, error) {
	return "fake probe", nil
}

func (f *fakeDOMBackend) ClickDOMButton(_ context.Context, labels []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, label := range labels {
		for _, b := range f.controls.Buttons {
			if !strings.EqualFold(b, label) {
				continue
			}
			f.clicks = append(f.clicks, label)
			switch strings.ToUpper(label) {
			case "START":
				f.controls.HasInput = true
				f.controls.ChatEnded = false
				f.controls.Buttons = []string{"Skip"}
				f.rows = nil
			case "SKIP":
				f.controls.HasInput = false
				f.controls.Buttons = []string{"START"}
				f.rows = nil
			}
			return label, nil
		}
	}
	return "", nil
}

func (f *fakeDOMBackend) addRow(author, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, browser.DOMMessage{Author: author, Text: text})
}

func (f *fakeDOMBackend) sentCount(text string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if s == text {
			n++
		}
	}
	return n
}

func (f *fakeDOMBackend) totalSent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeDOMBackend) clickCount(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.clicks {
		if strings.EqualFold(c, label) {
			n++
		}
	}
	return n
}

func waitForLive(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testLiveRunner(a *App, fake *fakeDOMBackend) *liveRunner {
	return &liveRunner{
		dom:          fake,
		prefix:       "[test show-live]",
		logf:         func(string, string) {},
		replyDelay:   time.Millisecond,
		pollInterval: 20 * time.Millisecond,
		seenCounts:   make(map[string]int),
		sentEcho:     make(map[string]int),
		echoAuthors:  make(map[string]int),
		newResponder: a.newLiveResponder,
	}
}

func TestLiveChatDOMFlow(t *testing.T) {
	a := browserTestApp(t)
	if err := a.SaveMessageSettings(model.NewMessageSettings("hello first\nreply line", "")); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: false, Buttons: []string{"START"}}}
	r := testLiveRunner(a, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	// Lobby: START is clicked automatically, greeting goes out.
	waitForLive(t, "greeting after START", func() bool { return fake.sentCount("hello first") == 1 })

	// Partner message is answered with the next sequence line.
	fake.addRow("partner", "hi")
	waitForLive(t, "reply", func() bool { return fake.sentCount("reply line") == 1 })

	// Our own echoed rows never trigger extra replies.
	time.Sleep(200 * time.Millisecond)
	if n := fake.totalSent(); n != 2 {
		t.Fatalf("own echo caused extra sends (total %d)", n)
	}

	// Sequence exhausted -> Skip is clicked, then a fresh START + greeting.
	fake.addRow("partner", "more")
	waitForLive(t, "skip click", func() bool { return fake.clickCount("Skip") >= 1 })
	waitForLive(t, "second greeting", func() bool { return fake.sentCount("hello first") >= 2 })

	// Partner leaves: bot returns to lobby and matches again.
	fake.mu.Lock()
	fake.controls.ChatEnded = true
	fake.controls.HasInput = false
	fake.controls.Buttons = []string{"START"}
	fake.rows = nil
	fake.mu.Unlock()
	waitForLive(t, "re-match after partner left", func() bool { return fake.clickCount("START") >= 3 })
}

func TestLiveFolderResponderPicksOneSequencePerConversation(t *testing.T) {
	a := browserTestApp(t)
	settings := model.NewMessageSettings("", "")
	settings.ChatMode = model.ChatModeFolder
	settings.LoadedSequences = []string{"greet one\nreply one", "greet two\nreply two"}
	settings.UseLoadedSequences = true
	if err := a.SaveMessageSettings(settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		responder, mode, err := a.newLiveResponder()
		if err != nil {
			t.Fatal(err)
		}
		if mode != model.ChatModeFolder {
			t.Fatalf("mode = %q", mode)
		}
		start := responder.Start()
		reply, end := responder.Reply("hi")
		if len(start) != 1 || len(reply) != 1 || end {
			t.Fatalf("bad flow: start=%v reply=%v end=%v", start, reply, end)
		}
		if (start[0] == "greet one") != (reply[0] == "reply one") {
			t.Fatalf("mixed sequences: %q then %q", start[0], reply[0])
		}
	}
}

func TestLiveChatEVAResponderStarts(t *testing.T) {
	a := browserTestApp(t)
	settings := model.NewMessageSettings("", "")
	settings.ChatMode = model.ChatModeEVAChat
	if err := a.SaveMessageSettings(settings); err != nil {
		t.Fatal(err)
	}
	responder, mode, err := a.newLiveResponder()
	if err != nil {
		t.Fatalf("EVA responder: %v", err)
	}
	if mode != "EVAChat Flow" {
		t.Fatalf("unexpected mode %q", mode)
	}
	if lines := responder.Start(); len(lines) == 0 {
		t.Fatal("EVA Start should produce a greeting line from the starter pack")
	}
}
