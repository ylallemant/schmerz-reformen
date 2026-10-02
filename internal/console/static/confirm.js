// Asks before a form that destroys something is sent.
//
// The question is in the form's own `data-confirm` attribute, so it is the
// template's and the translator's. It is asked from here rather than from an
// `onsubmit` attribute because the Content-Security-Policy refuses inline
// handlers — which is the policy working, not an obstacle to it.
//
// Without this script the form simply submits. That is the right way round to
// fail: every such form sits in a box marked as destructive, and the backend
// writes who did it to the audit log either way.
(function () {
  "use strict";

  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (!form || !form.dataset || !form.dataset.confirm) {
      return;
    }
    if (!window.confirm(form.dataset.confirm)) {
      event.preventDefault();
    }
  });
})();
