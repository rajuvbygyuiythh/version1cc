package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// DOM-only live chat surface for Show Mode. Everything happens through the
// visible page like a human would: read message rows from the DOM, type into
// the message box, click buttons. No WebSocket frames and no API calls are
// used here by design (user requirement, 2026-10-14). The heuristics are
// structural (time-stamped message rows, placeholder text, button labels)
// because the app uses utility CSS classes without semantic message names.

type DOMMessage struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

type DOMControls struct {
	HasInput  bool     `json:"hasInput"`
	InputDesc string   `json:"inputDesc"`
	Buttons   []string `json:"buttons"`
	ChatEnded bool     `json:"chatEnded"`
	Partner   string   `json:"partner"`
}

// DOMChatBackend is implemented by backends that can drive the page DOM
// (Rod). Optional interface; the core Backend contract is unchanged.
type DOMChatBackend interface {
	ExtractDOMMessages(ctx context.Context) ([]DOMMessage, error)
	DOMControls(ctx context.Context) (DOMControls, error)
	SendDOMMessage(ctx context.Context, text string) (bool, error)
	ClickDOMButton(ctx context.Context, labels []string) (string, error)
	DOMProbe(ctx context.Context) (string, error)
}

const domLibJS = `
const __smClean = (s) => (s || '').replace(/\s+/g, ' ').trim();
const __smVisible = (el) => {
  if (!el) return false;
  const r = el.getBoundingClientRect();
  const st = getComputedStyle(el);
  return r.width > 0 && r.height > 0 && st.display !== 'none' && st.visibility !== 'hidden';
};
const __smFindInput = () => {
  const els = Array.from(document.querySelectorAll('textarea, input[type="text"], input:not([type]), [contenteditable="true"], [contenteditable=""], [role="textbox"]')).filter(__smVisible);
  if (!els.length) return null;
  const byPlaceholder = els.find((el) => /message/i.test(el.getAttribute('placeholder') || '') || /message/i.test(el.getAttribute('aria-label') || ''));
  return byPlaceholder || els[els.length - 1];
};
const __smRows = () => {
  const timeRe = /\b\d{1,2}:\d{2}\s?(AM|PM)\b/i;
  const all = Array.from(document.querySelectorAll('div, li, section, article'));
  const parsed = [];
  for (const el of all) {
    const t = __smClean(el.innerText);
    if (!t || t.length > 600 || !timeRe.test(t)) continue;
    if (el.querySelectorAll('*').length > 40) continue;
    // Lines of the row: first is the "name 10:24 PM" line, the rest is the
    // message. Clock-only / tick-only lines are chrome, never content.
    const lines = (el.innerText || '').split('\n').map(__smClean).filter(Boolean)
      .filter((l) => !/^(today|yesterday)$/i.test(l))
      .filter((l) => !(/^[\d:APMapm \u2713\u2714]+$/.test(l) && l.length <= 12));
    let row = null;
    if (lines.length >= 2) {
      const author = __smClean(lines[0].replace(timeRe, '').replace(/[\u2713\u2714]/g, ''));
      const content = lines.slice(1).join(' ').trim();
      if (author && content) row = { author: author, text: content };
    } else if (lines.length === 1) {
      // Single-line layout: "name 10:24 PM hello text" or "name: hello text".
      const one = lines[0].replace(timeRe, ' ').replace(/[\u2713\u2714]/g, '').replace(/\s+/g, ' ').trim();
      const m = one.match(/^([^:]{1,40}):\s+(.+)$/);
      if (m) row = { author: __smClean(m[1]), text: m[2].trim() };
    }
    if (row) parsed.push([el, row]);
  }
  // A container that holds another accepted row is the chat list, not a row.
  return parsed
    .filter(([el]) => !parsed.some(([other]) => other !== el && el.contains(other)))
    .map(([, r]) => r)
    .slice(-40);
};
`

func (b *RodBackend) evalDOM(ctx context.Context, expr string, out any) error {
	return b.withPageRetry(ctx, func(page *rod.Page) error {
		res, err := page.Eval(expr)
		if err != nil {
			return err
		}
		if out == nil {
			return nil
		}
		if err := res.Value.Unmarshal(out); err != nil {
			return fmt.Errorf("decode DOM result: %w", err)
		}
		return nil
	})
}

// ExtractDOMMessages returns the currently rendered chat rows (oldest last
// rendered order as in the DOM, capped at the latest 40).
func (b *RodBackend) ExtractDOMMessages(ctx context.Context) ([]DOMMessage, error) {
	var rows []DOMMessage
	err := b.evalDOM(ctx, "() => { "+domLibJS+" return __smRows(); }", &rows)
	return rows, err
}

func (b *RodBackend) DOMControls(ctx context.Context) (DOMControls, error) {
	var controls DOMControls
	expr := `() => { ` + domLibJS + `
  const input = __smFindInput();
  const buttons = Array.from(document.querySelectorAll('button, [role="button"], a'))
    .filter(__smVisible)
    .map((el) => __smClean(el.innerText || el.getAttribute('aria-label') || ''))
    .filter(Boolean)
    .slice(0, 30);
  const bodyText = document.body ? document.body.innerText : '';
  const chatEnded = /left the chat|chat (has )?ended/i.test(bodyText);
  let partner = '';
  const pm = bodyText.match(/chatting with\s+@?([^\n]{2,60})/iu);
  if (pm) {
    const words = pm[1].trim().split(/\s+/);
    while (words.length > 1 && /^(skip|end|start|block|report|options|menu|new|chat)$/i.test(words[words.length - 1])) words.pop();
    partner = words.join(' ');
  }
  return {
    hasInput: !!input,
    inputDesc: input ? (input.tagName + ':' + (input.getAttribute('placeholder') || input.getAttribute('aria-label') || '')) : '',
    buttons: buttons,
    chatEnded: chatEnded,
    partner: partner
  };
}`
	err := b.evalDOM(ctx, expr, &controls)
	return controls, err
}

// ClickDOMButton clicks the first visible button whose label matches one of
// labels (exact match preferred, then substring; case-insensitive). Returns
// the clicked label text, or "" when nothing matched.
func (b *RodBackend) ClickDOMButton(ctx context.Context, labels []string) (string, error) {
	var out struct {
		Clicked string `json:"clicked"`
	}
	expr := `(labels) => { ` + domLibJS + `
  const els = Array.from(document.querySelectorAll('button, [role="button"], a')).filter(__smVisible);
  const norm = (s) => __smClean(s).toLowerCase();
  const wanted = labels.map(norm);
  for (const w of wanted) {
    const exact = els.find((el) => norm(el.innerText || el.getAttribute('aria-label') || '') === w);
    if (exact) { exact.click(); return { clicked: exact.innerText || w }; }
  }
  for (const w of wanted) {
    const part = els.find((el) => norm(el.innerText || el.getAttribute('aria-label') || '').includes(w));
    if (part) { part.click(); return { clicked: part.innerText || w }; }
  }
  return { clicked: '' };
}`
	err := b.evalDOMArgs(ctx, expr, []any{labels}, &out)
	return strings.TrimSpace(out.Clicked), err
}

// SendDOMMessage types text into the chat box and sends it (send button when
// identifiable, otherwise Enter), then verifies that the text shows up as a
// rendered row. The boolean reports verification, not just the attempt.
func (b *RodBackend) SendDOMMessage(ctx context.Context, text string) (bool, error) {
	var prep struct {
		OK bool `json:"ok"`
	}
	insertExpr := `(text) => { ` + domLibJS + `
  const el = __smFindInput();
  if (!el) return { ok: false };
  el.focus();
  el.click();
  let done = false;
  try { done = document.execCommand('insertText', false, text); } catch (e) { done = false; }
  if (!done) {
    try {
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype
        : (el instanceof HTMLInputElement ? HTMLInputElement.prototype : null);
      const setter = proto ? Object.getOwnPropertyDescriptor(proto, 'value') : null;
      if (setter && setter.set) setter.set.call(el, text);
      else if ('value' in el) el.value = text;
      else el.innerText = text;
      el.dispatchEvent(new InputEvent('input', { bubbles: true, data: text, inputType: 'insertText' }));
    } catch (e) { /* fall through */ }
  }
  return { ok: true };
}`
	if err := b.evalDOMArgs(ctx, insertExpr, []any{text}, &prep); err != nil {
		return false, err
	}
	if !prep.OK {
		return false, nil
	}

	var send struct {
		Clicked string `json:"clicked"`
	}
	sendExpr := `() => { ` + domLibJS + `
  const el = __smFindInput();
  if (el) {
    let node = el;
    for (let i = 0; i < 7 && node; i++) {
      node = node.parentElement;
      if (!node) break;
      const btns = Array.from(node.querySelectorAll('button, [role="button"], input[type="submit"]')).filter(__smVisible);
      const sendBtn = btns.find((b) => /send/i.test(b.getAttribute('aria-label') || '') || /send/i.test(b.innerText || '') || b.type === 'submit');
      if (sendBtn) { sendBtn.click(); return { clicked: 'send-button' }; }
    }
    const form = el.closest('form');
    if (form) { form.requestSubmit ? form.requestSubmit() : form.submit(); return { clicked: 'form-submit' }; }
    for (const type of ['keydown', 'keypress', 'keyup']) {
      el.dispatchEvent(new KeyboardEvent(type, { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true, cancelable: true }));
    }
    return { clicked: 'enter' };
  }
  return { clicked: '' };
}`
	if err := b.evalDOM(ctx, sendExpr, &send); err != nil {
		return false, err
	}
	if send.Clicked == "" {
		return false, nil
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := b.ExtractDOMMessages(ctx)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.Text == strings.TrimSpace(text) {
				return true, nil
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return false, nil
}

// DOMProbe returns a short diagnostic summary of what the bot can see, used
// for logs when the chat UI cannot be identified.
func (b *RodBackend) DOMProbe(ctx context.Context) (string, error) {
	var probe struct {
		URL     string   `json:"url"`
		Inputs  []string `json:"inputs"`
		Buttons []string `json:"buttons"`
		Rows    int      `json:"rows"`
	}
	expr := `() => { ` + domLibJS + `
  const inputs = Array.from(document.querySelectorAll('textarea, input, [contenteditable], [role="textbox"]'))
    .filter(__smVisible)
    .map((el) => el.tagName + ':' + (el.getAttribute('placeholder') || el.getAttribute('aria-label') || el.type || ''))
    .slice(0, 8);
  const buttons = Array.from(document.querySelectorAll('button, [role="button"], a'))
    .filter(__smVisible)
    .map((el) => __smClean(el.innerText || el.getAttribute('aria-label') || ''))
    .filter(Boolean)
    .slice(0, 20);
  return { url: location.href, inputs: inputs, buttons: buttons, rows: __smRows().length };
}`
	if err := b.evalDOM(ctx, expr, &probe); err != nil {
		return "", err
	}
	return fmt.Sprintf("url=%s inputs=%v buttons=%v messageRows=%d", probe.URL, probe.Inputs, probe.Buttons, probe.Rows), nil
}

// evalDOMArgs is evalDOM with JavaScript arguments (marshalled to JSON).
func (b *RodBackend) evalDOMArgs(ctx context.Context, expr string, args []any, out any) error {
	return b.withPageRetry(ctx, func(page *rod.Page) error {
		res, err := page.Eval(expr, args...)
		if err != nil {
			return err
		}
		if out == nil {
			return nil
		}
		if err := res.Value.Unmarshal(out); err != nil {
			return fmt.Errorf("decode DOM result: %w", err)
		}
		return nil
	})
}
