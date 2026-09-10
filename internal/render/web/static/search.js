// Spec §10: a build-time index over titles, headings and body text, queried
// by a small vanilla-JS client. No server component (spec §15).
(function () {
  var script = document.currentScript;
  var root = (script && script.getAttribute('data-root')) || '';
  var input = document.getElementById('q');
  var list = document.getElementById('results');
  if (!input || !list) { return; }

  var index = null;
  // pending memoises the in-flight fetch. The previous version tracked it with
  // a boolean and returned Promise.resolve() while the request was still
  // running, so run() executed with index === null and index.length threw a
  // TypeError. It was not an edge case: keystroke 1 ("p") starts the fetch and
  // returns early on q.length < 2, keystroke 2 ("pa") arrives mid-flight and
  // gets the already-resolved promise. On a LAN-hosted portal that window is
  // wide enough that the first search on a cold page reliably fails, and it
  // self-heals on the next keystroke — so it read as "search is flaky" rather
  // than as a bug.
  var pending = null;
  // failed records that the index could not be loaded, which is a different
  // answer from a query that matched nothing. Both used to render as an empty
  // list, so a reader could not tell "no such service" from "search is
  // broken" (spec §12: a degraded mode must be visible in the artifact).
  var failed = false;

  function load() {
    if (index) { return Promise.resolve(); }
    if (!pending) {
      pending = fetch(root + 'search-index.json')
        .then(function (r) {
          if (!r.ok) { throw new Error('search-index.json: HTTP ' + r.status); }
          return r.json();
        })
        .then(function (data) { index = data; failed = false; })
        .catch(function () { index = []; failed = true; });
    }
    return pending;
  }

  function score(entry, terms) {
    var title = (entry.title || '').toLowerCase();
    var headings = (entry.headings || []).join(' ').toLowerCase();
    var body = ((entry.description || '') + ' ' + (entry.text || '')).toLowerCase();
    var owner = (entry.owner || '').toLowerCase();
    var total = 0;
    for (var i = 0; i < terms.length; i++) {
      var t = terms[i];
      var hit = 0;
      if (title.indexOf(t) !== -1) { hit += title === t ? 100 : 40; }
      if (owner.indexOf(t) !== -1) { hit += 20; }
      if (headings.indexOf(t) !== -1) { hit += 10; }
      if (body.indexOf(t) !== -1) { hit += 2; }
      // Every term must appear somewhere, so "payments runbook" does not
      // match a page that only mentions payments.
      if (hit === 0) { return 0; }
      total += hit;
    }
    return total;
  }

  // note puts one plain line in the result list. It exists so a state that is
  // not "these are your results" can still say what it is, in the place the
  // reader is already looking.
  function note(text) {
    list.textContent = '';
    var li = document.createElement('li');
    li.className = 'result-note';
    li.textContent = text;
    list.appendChild(li);
    list.hidden = false;
  }

  function render(results) {
    list.textContent = '';
    if (!results.length) {
      list.hidden = true;
      return;
    }
    results.forEach(function (entry) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.href = root + entry.url;
      // textContent, NEVER innerHTML. This text came from somebody's
      // Markdown. It was escaped on its way into the HTML pages because
      // goldmark runs without WithUnsafe (spec §14.1) — but this arrived as
      // JSON, unescaped, and innerHTML here would reopen exactly that hole.
      a.textContent = entry.title || entry.url;
      li.appendChild(a);
      if (entry.kind) {
        var span = document.createElement('span');
        span.className = 'result-kind';
        span.textContent = entry.kind;
        li.appendChild(span);
      }
      list.appendChild(li);
    });
    list.hidden = false;
  }

  function run() {
    var q = input.value.trim().toLowerCase();
    if (q.length < 2) {
      render([]);
      return;
    }
    if (failed) {
      // "Nothing matched" and "the index never loaded" are different answers
      // and must not both render as an empty list. Checked here rather than
      // in render() so an empty search box still shows nothing at all.
      note('Search is unavailable: search-index.json did not load.');
      return;
    }
    var terms = q.split(/\s+/);
    var scored = [];
    for (var i = 0; i < index.length; i++) {
      var s = score(index[i], terms);
      if (s > 0) { scored.push({ s: s, e: index[i] }); }
    }
    scored.sort(function (a, b) { return b.s - a.s; });
    render(scored.slice(0, 10).map(function (x) { return x.e; }));
  }

  input.addEventListener('input', function () {
    load().then(run);
  });
  input.addEventListener('blur', function () {
    // A click on a result must land before the list disappears.
    setTimeout(function () { list.hidden = true; }, 150);
  });
})();
