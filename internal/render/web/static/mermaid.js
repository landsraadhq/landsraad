// Spec §14.1: Mermaid renders in strict mode.
//
// The bundle may legitimately be absent — `--mermaid-src none`, a portal on
// a host with no outbound network, or a failed SRI check. In that case every
// diagram block is replaced by a visible note — never left showing a wall of
// raw Mermaid source (spec §12: a degraded mode must be visible in the
// artifact) — with the original source kept in a collapsed <details> so
// someone debugging the missing bundle can still get at it.
(function () {
  var blocks = document.querySelectorAll('pre.mermaid');
  if (!blocks.length) { return; }

  if (!window.mermaid) {
    blocks.forEach(function (el) {
      var source = el.textContent;

      var wrapper = document.createElement('div');
      wrapper.className = 'mermaid-unavailable';

      var details = document.createElement('details');
      var summary = document.createElement('summary');
      summary.textContent = 'diagram not rendered: the Mermaid bundle did not load';
      var pre = document.createElement('pre');
      pre.textContent = source;

      details.appendChild(summary);
      details.appendChild(pre);
      wrapper.appendChild(details);
      el.replaceWith(wrapper);
    });
    return;
  }
  window.mermaid.initialize({
    startOnLoad: true,
    securityLevel: 'strict',
    theme: 'neutral'
  });
})();
