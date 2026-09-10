// Spec §10: the catalog is filterable by kind/team/tier/tag and sortable.
//
// Every row is already in the HTML with its data- attributes, so with
// JavaScript disabled the reader still gets the whole catalog sorted by
// ref. That is a worse experience, not a broken page — which is why this
// was never going to be a query parameter and a rebuild.
(function () {
  var table = document.getElementById('catalog');
  var filters = document.getElementById('filters');
  if (!table) { return; }
  var tbody = table.tBodies[0];
  var shown = document.getElementById('shown');

  function rows() {
    return Array.prototype.slice.call(tbody.rows);
  }

  function applyFilters() {
    if (!filters) { return; }
    var selects = filters.querySelectorAll('select[data-filter]');
    var visible = 0;
    rows().forEach(function (row) {
      var ok = true;
      selects.forEach(function (sel) {
        var want = sel.value;
        if (!want) { return; }
        var have = row.getAttribute('data-' + sel.getAttribute('data-filter')) || '';
        if (sel.getAttribute('data-filter') === 'tags') {
          // data-tags is a space-separated list, so match a whole token:
          // the tag "go" must not match "golang".
          ok = ok && (' ' + have + ' ').indexOf(' ' + want + ' ') !== -1;
        } else {
          ok = ok && have === want;
        }
      });
      row.hidden = !ok;
      if (ok) { visible++; }
    });
    if (shown) { shown.textContent = String(visible); }
  }

  function cellValue(row, index, kind) {
    var cell = row.cells[index];
    if (!cell) { return kind === 'number' ? 0 : ''; }
    if (kind === 'number') {
      // data-value carries the sortable number: "not scored" is -1 so the
      // unscored entities group at one end rather than sorting between 9%
      // and 90% as text would.
      return parseFloat(cell.getAttribute('data-value') || '0');
    }
    return (cell.textContent || '').trim().toLowerCase();
  }

  function sortBy(index, kind, ascending) {
    var sorted = rows().sort(function (a, b) {
      var x = cellValue(a, index, kind);
      var y = cellValue(b, index, kind);
      if (x < y) { return ascending ? -1 : 1; }
      if (x > y) { return ascending ? 1 : -1; }
      return 0;
    });
    sorted.forEach(function (row) { tbody.appendChild(row); });
  }

  Array.prototype.slice.call(table.tHead.rows[0].cells).forEach(function (th, index) {
    var kind = th.getAttribute('data-sort');
    if (!kind) { return; }
    th.tabIndex = 0;
    th.classList.add('sortable');
    var ascending = true;
    function activate() {
      sortBy(index, kind, ascending);
      Array.prototype.slice.call(table.tHead.rows[0].cells).forEach(function (other) {
        other.removeAttribute('aria-sort');
      });
      th.setAttribute('aria-sort', ascending ? 'ascending' : 'descending');
      ascending = !ascending;
    }
    th.addEventListener('click', activate);
    th.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        activate();
      }
    });
  });

  if (filters) {
    filters.addEventListener('change', applyFilters);
  }
})();
