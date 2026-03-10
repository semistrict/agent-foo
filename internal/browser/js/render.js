// Collect visible elements with bounding boxes in the viewport.
// Returns an array of {tag, text, role, ref, x, y, w, h, interactive}.
(() => {
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  const interactive = new Set([
    'A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA', 'DETAILS', 'SUMMARY',
  ]);
  const interactiveRoles = new Set([
    'button', 'link', 'textbox', 'checkbox', 'radio', 'combobox',
    'menuitem', 'tab', 'switch', 'slider', 'spinbutton', 'searchbox',
    'option', 'listbox', 'menu',
  ]);
  const landmark = new Set([
    'HEADER', 'NAV', 'MAIN', 'FOOTER', 'ASIDE', 'SECTION', 'ARTICLE',
  ]);
  const skip = new Set([
    'HTML', 'HEAD', 'BODY', 'SCRIPT', 'STYLE', 'NOSCRIPT', 'META',
    'LINK', 'BR', 'WBR',
  ]);

  const results = [];
  const seen = new Set();

  function visible(el) {
    const style = getComputedStyle(el);
    return style.display !== 'none' &&
           style.visibility !== 'hidden' &&
           style.opacity !== '0' &&
           el.offsetWidth > 0 &&
           el.offsetHeight > 0;
  }

  function label(el) {
    // aria-label first
    const aria = el.getAttribute('aria-label');
    if (aria) return aria.slice(0, 40);

    // For inputs, use placeholder or value
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA') {
      return el.placeholder || el.value || el.type || '';
    }

    // Direct text content (not from children)
    let text = '';
    for (const node of el.childNodes) {
      if (node.nodeType === 3) text += node.textContent;
    }
    text = text.trim().slice(0, 40);
    if (text) return text;

    // alt text for images
    if (el.tagName === 'IMG') return el.alt || el.src.split('/').pop().slice(0, 30) || 'img';

    return '';
  }

  function shouldInclude(el) {
    const tag = el.tagName;
    if (skip.has(tag)) return false;
    if (interactive.has(tag)) return true;
    if (landmark.has(tag)) return true;
    if (tag === 'IMG' || tag === 'SVG' || tag === 'VIDEO') return true;
    if (tag === 'H1' || tag === 'H2' || tag === 'H3' || tag === 'H4' || tag === 'H5' || tag === 'H6') return true;

    const role = el.getAttribute('role');
    if (role && interactiveRoles.has(role)) return true;
    if (role === 'banner' || role === 'navigation' || role === 'main' || role === 'contentinfo') return true;

    // Clickable divs/spans
    if (el.onclick || el.getAttribute('tabindex') === '0') return true;

    // Elements that already have a ref from snapshot
    if (el.hasAttribute('data-ref')) return true;

    // Leaf text containers only if they have substantial direct text
    if (tag === 'P' || tag === 'LI' || tag === 'LABEL') {
      let text = '';
      for (const node of el.childNodes) {
        if (node.nodeType === 3) text += node.textContent;
      }
      if (text.trim().length > 3) return true;
    }

    return false;
  }

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
  let el;
  while (el = walker.nextNode()) {
    if (!shouldInclude(el)) continue;
    if (!visible(el)) continue;

    const rect = el.getBoundingClientRect();
    // Must overlap viewport
    if (rect.right < 0 || rect.bottom < 0 || rect.left > vw || rect.top > vh) continue;
    // Minimum size
    if (rect.width < 4 || rect.height < 4) continue;

    // Dedupe by position (skip elements at same spot with same size)
    const key = `${Math.round(rect.left)},${Math.round(rect.top)},${Math.round(rect.width)},${Math.round(rect.height)}`;
    if (seen.has(key)) continue;
    seen.add(key);

    const role = el.getAttribute('role') || '';
    const tag = el.tagName.toLowerCase();
    const isInteractive = interactive.has(el.tagName) || interactiveRoles.has(role);
    const ref = el.getAttribute('data-ref') || '';

    results.push({
      tag: tag,
      text: label(el),
      role: role || undefined,
      ref: ref || undefined,
      x: Math.max(0, Math.round(rect.left)),
      y: Math.max(0, Math.round(rect.top)),
      w: Math.round(rect.width),
      h: Math.round(rect.height),
      interactive: isInteractive || undefined,
    });
  }

  return JSON.stringify({
    viewport: {w: vw, h: vh},
    elements: results,
  });
})()
