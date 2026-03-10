// Add visual ref annotations to all interactive elements with data-ref attributes.
// Returns the overlay container ID for later removal.
(() => {
  const id = '__af_refs_overlay__';
  // Remove any existing overlay
  const existing = document.getElementById(id);
  if (existing) existing.remove();

  const container = document.createElement('div');
  container.id = id;
  container.style.cssText = 'position:fixed;top:0;left:0;width:100%;height:100%;pointer-events:none;z-index:2147483647;';
  document.body.appendChild(container);

  const interactive = new Set([
    'A', 'BUTTON', 'INPUT', 'SELECT', 'TEXTAREA', 'DETAILS', 'SUMMARY',
  ]);
  const interactiveRoles = new Set([
    'button', 'link', 'textbox', 'checkbox', 'radio', 'combobox',
    'menuitem', 'tab', 'switch', 'slider', 'spinbutton', 'searchbox',
    'option', 'listbox', 'menu',
  ]);

  const els = document.querySelectorAll('[data-ref]');
  for (const el of els) {
    const role = el.getAttribute('role') || '';
    const isInteractive = interactive.has(el.tagName) || interactiveRoles.has(role) ||
      el.onclick || el.getAttribute('tabindex') === '0';
    if (!isInteractive) continue;

    const style = getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') continue;

    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) continue;
    if (rect.right < 0 || rect.bottom < 0 || rect.left > window.innerWidth || rect.top > window.innerHeight) continue;

    const ref = el.getAttribute('data-ref');

    // Bounding box
    const box = document.createElement('div');
    box.style.cssText = `position:fixed;left:${rect.left}px;top:${rect.top}px;width:${rect.width}px;height:${rect.height}px;border:2px solid rgba(255,0,0,0.8);box-sizing:border-box;pointer-events:none;`;
    container.appendChild(box);

    // Label
    const label = document.createElement('div');
    label.textContent = '@' + ref;
    label.style.cssText = `position:fixed;left:${rect.left}px;top:${Math.max(0, rect.top - 16)}px;background:rgba(255,0,0,0.85);color:#fff;font:bold 11px/14px monospace;padding:0 3px;pointer-events:none;white-space:nowrap;border-radius:2px;`;
    container.appendChild(label);
  }

  return id;
})()
