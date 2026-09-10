/* Client-side search over the generated /search.json index.
 *
 * Deliberately dependency-free and small: the index is fetched once, lazily, on
 * first keystroke, and ranking is a weighted substring score. This is intended
 * for sites of a few hundred pages, where a full inverted index is not worth
 * the payload.
 */
(function () {
  "use strict";

  var input = document.getElementById("site-search-input");
  var results = document.getElementById("site-search-results");
  if (!input || !results) return;

  var indexURL = input.getAttribute("data-index") || "/search.json";
  var docs = null;
  var pending = null;
  var cursor = -1;
  var MAX_RESULTS = 8;

  function loadIndex() {
    if (docs) return Promise.resolve(docs);
    if (!pending) {
      pending = fetch(indexURL, { headers: { Accept: "application/json" } })
        .then(function (res) {
          if (!res.ok) throw new Error("HTTP " + res.status);
          return res.json();
        })
        .then(function (data) {
          // Accept either a bare array or {generated, documents:[...]}.
          docs = Array.isArray(data) ? data : data.documents || [];
          return docs;
        })
        .catch(function () {
          // A missing index should degrade to "no results", never a thrown error.
          docs = [];
          return docs;
        });
    }
    return pending;
  }

  function score(doc, needle) {
    var total = 0;
    var title = (doc.title || "").toLowerCase();
    var body = (doc.body || "").toLowerCase();
    var tags = (doc.tags || []).join(" ").toLowerCase();

    if (title.indexOf(needle) === 0) total += 12;
    else if (title.indexOf(needle) !== -1) total += 7;
    if (tags.indexOf(needle) !== -1) total += 4;
    if (body.indexOf(needle) !== -1) total += 2;
    return total;
  }

  function excerpt(doc, needle) {
    var body = doc.body || "";
    var at = body.toLowerCase().indexOf(needle);
    if (at === -1) return body.slice(0, 120);
    var start = Math.max(0, at - 40);
    var end = Math.min(body.length, at + needle.length + 90);
    return (start > 0 ? "…" : "") + body.slice(start, end) + (end < body.length ? "…" : "");
  }

  function render(query) {
    cursor = -1;
    if (!query) {
      results.hidden = true;
      results.innerHTML = "";
      return;
    }

    loadIndex().then(function (all) {
      var needle = query.toLowerCase();
      var matches = all
        .map(function (doc) {
          return { doc: doc, score: score(doc, needle) };
        })
        .filter(function (m) {
          return m.score > 0;
        })
        .sort(function (a, b) {
          return b.score - a.score || (a.doc.title || "").localeCompare(b.doc.title || "");
        })
        .slice(0, MAX_RESULTS);

      results.innerHTML = "";
      if (matches.length === 0) {
        var none = document.createElement("div");
        none.className = "no-results";
        none.textContent = "No results for “" + query + "”";
        results.appendChild(none);
      } else {
        matches.forEach(function (m) {
          var link = document.createElement("a");
          link.href = m.doc.url;
          var title = document.createElement("span");
          title.className = "result-title";
          title.textContent = m.doc.title || m.doc.url;
          var ex = document.createElement("span");
          ex.className = "result-excerpt";
          ex.textContent = excerpt(m.doc, needle);
          link.appendChild(title);
          link.appendChild(ex);
          results.appendChild(link);
        });
      }
      results.hidden = false;
    });
  }

  function links() {
    return Array.prototype.slice.call(results.querySelectorAll("a"));
  }

  function move(delta) {
    var items = links();
    if (items.length === 0) return;
    items.forEach(function (el) {
      el.classList.remove("is-active");
    });
    cursor = (cursor + delta + items.length) % items.length;
    items[cursor].classList.add("is-active");
    items[cursor].scrollIntoView({ block: "nearest" });
  }

  var timer = null;
  input.addEventListener("input", function () {
    var value = input.value.trim();
    // Debounce so fast typing does not re-render the list on every character.
    clearTimeout(timer);
    timer = setTimeout(function () {
      render(value.length < 2 ? "" : value);
    }, 90);
  });

  input.addEventListener("keydown", function (event) {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      move(1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      move(-1);
    } else if (event.key === "Enter") {
      var items = links();
      if (cursor >= 0 && items[cursor]) {
        event.preventDefault();
        window.location.href = items[cursor].href;
      }
    } else if (event.key === "Escape") {
      results.hidden = true;
      input.blur();
    }
  });

  document.addEventListener("click", function (event) {
    if (!results.contains(event.target) && event.target !== input) {
      results.hidden = true;
    }
  });
})();
