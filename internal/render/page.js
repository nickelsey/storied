(function () {
  var sections = Array.prototype.slice.call(document.querySelectorAll(".story-section"));
  var links = {};
  document.querySelectorAll(".outline a").forEach(function (a) {
    links[a.getAttribute("href").slice(1)] = a;
  });
  function current() {
    var at = 0;
    sections.forEach(function (s, i) {
      if (s.getBoundingClientRect().top <= 80) at = i;
    });
    return at;
  }
  function mark() {
    var id = sections.length ? sections[current()].id : "";
    Object.keys(links).forEach(function (k) {
      links[k].classList.toggle("current", k === id);
    });
    sections.forEach(function (s) {
      s.classList.toggle("current", s.id === id);
    });
  }
  document.addEventListener("keydown", function (e) {
    if (e.target.closest("input, textarea, select") || e.metaKey || e.ctrlKey || e.altKey) return;
    var step = e.key === "j" ? 1 : e.key === "k" ? -1 : 0;
    if (!step || !sections.length) return;
    var next = Math.min(sections.length - 1, Math.max(0, current() + step));
    sections[next].scrollIntoView();
  });
  document.addEventListener("scroll", mark, { passive: true });
  mark();
})();
