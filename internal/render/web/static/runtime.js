// Spec §10: the page fetches runtime.json client-side and degrades to
// "runtime unknown" when it is absent.
//
// v1 does not produce that file — the runtime agent is sub-project E — so
// today this ALWAYS degrades, and that is the point. E must require no
// portal change, and the only way to know the claim is true is to ship the
// consumer before the producer.
//
// The badge already reads "runtime unknown" from the server. This script
// only ever replaces that text on a successful fetch: a network error must
// not turn a truthful "unknown" into a blank space.
(function () {
  var script = document.currentScript;
  var root = (script && script.getAttribute('data-root')) || '';
  var badges = document.querySelectorAll('.runtime[data-ref]');
  if (!badges.length) { return; }

  fetch(root + 'runtime.json', { cache: 'no-store' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      if (!data) { return; }
      badges.forEach(function (el) {
        var info = data[el.getAttribute('data-ref')];
        if (!info || !info.status) { return; }
        el.textContent = info.status;
        el.classList.add('runtime-known');
      });
    })
    .catch(function () {
      // Absent is the expected case in v1. The badge already says so.
    });
})();
