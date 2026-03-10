package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// SnapshotOptions controls snapshot output.
type SnapshotOptions struct {
	InteractiveOnly bool
	Compact         bool
	Depth           int
}

// Snapshot walks the DOM, injects data-ref attributes on elements, and returns
// an accessibility-like tree as text.
func Snapshot(ctx context.Context, opts SnapshotOptions) (string, error) {
	// This JS walks the DOM, assigns data-ref to each meaningful element,
	// and returns the tree. Refs persist on the DOM so @ref selectors work.
	js := `(() => {
		// Clear old refs first
		document.querySelectorAll('[data-ref]').forEach(el => el.removeAttribute('data-ref'));

		const interactiveRoles = new Set(['button','link','textbox','combobox','checkbox','radio','slider','spinbutton','switch','tab','menuitem','option','searchbox','textarea']);
		const interactiveTags = new Set(['A','BUTTON','INPUT','SELECT','TEXTAREA']);
		const results = [];
		let ref = 0;

		function walk(el) {
			const role = el.getAttribute('role') || tagToRole(el);
			const name = el.getAttribute('aria-label')
				|| el.getAttribute('alt')
				|| el.getAttribute('title')
				|| el.getAttribute('placeholder')
				|| (el.tagName === 'A' || el.tagName === 'BUTTON' ? el.textContent?.trim().slice(0, 80) : '')
				|| '';

			const isInteractive = interactiveRoles.has(role) || interactiveTags.has(el.tagName);

			if (role && role !== 'generic' && role !== 'none') {
				ref++;
				const refId = 'e' + ref;
				el.setAttribute('data-ref', refId);
				results.push({ ref: refId, role, name: name.replace(/\s+/g, ' ').trim(), interactive: isInteractive });
			}

			for (const child of el.children) {
				walk(child);
			}
		}

		function tagToRole(el) {
			const map = {
				'A': 'link', 'BUTTON': 'button', 'INPUT': inputRole(el),
				'SELECT': 'combobox', 'TEXTAREA': 'textarea',
				'H1': 'heading', 'H2': 'heading', 'H3': 'heading',
				'H4': 'heading', 'H5': 'heading', 'H6': 'heading',
				'NAV': 'navigation', 'MAIN': 'main', 'HEADER': 'banner',
				'FOOTER': 'contentinfo', 'ASIDE': 'complementary',
				'UL': 'list', 'OL': 'list', 'LI': 'listitem',
				'TABLE': 'table', 'IMG': 'img', 'FORM': 'form',
			};
			return map[el.tagName] || '';
		}

		function inputRole(el) {
			const t = (typeof el.type === 'string' ? el.type : 'text').toLowerCase();
			if (t === 'checkbox') return 'checkbox';
			if (t === 'radio') return 'radio';
			if (t === 'range') return 'slider';
			if (t === 'search') return 'searchbox';
			return 'textbox';
		}

		walk(document.body);
		return JSON.stringify(results);
	})()`

	var resultJSON string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &resultJSON)); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}

	var nodes []struct {
		Ref         string `json:"ref"`
		Role        string `json:"role"`
		Name        string `json:"name"`
		Interactive bool   `json:"interactive"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &nodes); err != nil {
		return "", fmt.Errorf("parse snapshot: %w", err)
	}

	var b strings.Builder
	for _, n := range nodes {
		if opts.InteractiveOnly && !n.Interactive {
			continue
		}
		if opts.Compact && n.Name == "" {
			continue
		}
		if n.Name != "" {
			fmt.Fprintf(&b, "@%s %s %q\n", n.Ref, n.Role, n.Name)
		} else {
			fmt.Fprintf(&b, "@%s %s\n", n.Ref, n.Role)
		}
	}
	return b.String(), nil
}
