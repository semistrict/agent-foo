// Convert page content to Markdown using Turndown.
// Expects turndown.js and turndown-plugin-gfm.js to be injected before this script.
(() => {
  const svc = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
  });

  // Enable GFM tables, strikethrough, etc.
  if (typeof turndownPluginGfm !== 'undefined') {
    svc.use(turndownPluginGfm.gfm);
  }

  // Remove script, style, noscript, svg, and other non-content elements
  svc.remove(['script', 'style', 'noscript', 'svg', 'link', 'meta']);

  // Skip hidden elements
  svc.addRule('hidden', {
    filter: (node) => {
      if (node.nodeType !== 1) return false;
      const style = window.getComputedStyle(node);
      return style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0';
    },
    replacement: () => '',
  });

  // Unwrap layout tables (tables used for positioning, not data).
  // A table is considered a layout table if it has no <th> elements.
  svc.addRule('layoutTables', {
    filter: (node) => {
      if (node.nodeName !== 'TABLE') return false;
      return !node.querySelector('th');
    },
    replacement: (content) => content.trim() + '\n\n',
  });
  svc.addRule('layoutTableRows', {
    filter: (node) => {
      if (node.nodeName !== 'TR' && node.nodeName !== 'TBODY' &&
          node.nodeName !== 'THEAD' && node.nodeName !== 'TFOOT') return false;
      const table = node.closest('table');
      return table && !table.querySelector('th');
    },
    replacement: (content) => content.trim() + '\n',
  });
  svc.addRule('layoutTableCells', {
    filter: (node) => {
      if (node.nodeName !== 'TD') return false;
      const table = node.closest('table');
      return table && !table.querySelector('th');
    },
    replacement: (content) => content.trim() ? content.trim() + ' ' : '',
  });

  // Convert <img> to markdown with alt text
  svc.addRule('images', {
    filter: 'img',
    replacement: (content, node) => {
      const alt = node.getAttribute('alt') || '';
      const src = node.getAttribute('src') || '';
      return `![${alt}](${src})`;
    },
  });

  // Use document.body or fall back to documentElement
  const root = document.body || document.documentElement;
  return svc.turndown(root.innerHTML);
})()
