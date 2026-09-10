// Spec §14.1: Mermaid renders in strict mode.
//
// The bundle may legitimately be absent — `--mermaid-src none`, a portal on
// a host with no outbound network, or a failed SRI check. In that case every
// diagram block is labelled rather than left as a wall of raw Mermaid
// source: spec §12, a degraded mode must be visible in the artifact.
(function () {
  var blocks = document.querySelectorAll('pre.mermaid');
  if (!blocks.length) { return; }

  if (!window.mermaid) {
    blocks.forEach(function (el) {
      el.classList.add('mermaid-unavailable');
      el.setAttribute('data-note', 'diagram not rendered: the Mermaid bundle did not load');
    });
    return;
  }
  window.mermaid.initialize({
    startOnLoad: true,
    securityLevel: 'strict',
    theme: 'neutral'
  });
})();
