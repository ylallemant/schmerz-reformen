/*
 * The language a page is read in.
 *
 * The visitor's choice lives in localStorage. With none stored, the browser's
 * own language settings decide, which the server reads from Accept-Language.
 *
 * The server renders every page and never sees localStorage, so the choice is
 * mirrored into a cookie it does see. This script keeps the two in step, and
 * runs synchronously in the <head>: a page that turns out to be in the wrong
 * language is reloaded before it has been drawn, rather than after somebody
 * started reading it.
 *
 * Without this script the switcher is an ordinary form posted to /language,
 * which sets the cookie on the server's side. Either way the address never
 * carries a language: a link somebody sends is read in the receiver's language.
 */
(function () {
  "use strict";

  var KEY = "schmerz-lang";
  var COOKIE = "schmerz_language";
  var GUARD = "schmerz-lang-reloaded";

  var root = document.documentElement;
  var offered = (root.getAttribute("data-languages") || "").split(" ");

  // The stored choice; null when there is none; undefined when storage cannot
  // be read at all — a private window, blocked site data — in which case the
  // cookie is the only memory there is and is left alone.
  function stored() {
    try {
      var value = localStorage.getItem(KEY);
      return offered.indexOf(value) >= 0 ? value : null;
    } catch (e) {
      return undefined;
    }
  }

  function cookie() {
    var match = document.cookie.match(new RegExp("(?:^|; )" + COOKIE + "=([^;]*)"));
    return match ? match[1] : null;
  }

  function writeCookie(value, age) {
    document.cookie = COOKIE + "=" + value + "; path=/; max-age=" + age + "; samesite=lax" +
      (location.protocol === "https:" ? "; secure" : "");
  }

  // A reload to bring the page in step, at most once per page and choice: if
  // the cookie cannot be written — cookies blocked — a page that reloaded
  // itself until it matched would never stop.
  function reloadOnce(choice) {
    var mark = location.href + "|" + choice;
    try {
      if (sessionStorage.getItem(GUARD) === mark) {
        return;
      }
      sessionStorage.setItem(GUARD, mark);
    } catch (e) {
      return;
    }
    location.reload();
  }

  // --- on every page: bring the cookie in step with the stored choice -------

  var choice = stored();
  if (choice) {
    // Renewed on every page, so a choice outlives the cookie's expiry.
    writeCookie(choice, 31536000);
    if (root.lang !== choice) {
      reloadOnce(choice);
    }
  } else if (choice === null && cookie() !== null) {
    // No choice stored, so the browser decides. A cookie left over — from a
    // browser without the script, or site data half cleared — would otherwise
    // keep overriding it.
    writeCookie("", 0);
    reloadOnce("browser");
  }

  // --- the switcher -----------------------------------------------------------

  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (!form || !form.hasAttribute("data-language-switch")) {
      return;
    }
    var button = event.submitter;
    var wanted = button && button.value;
    if (offered.indexOf(wanted) < 0) {
      return;
    }

    try {
      localStorage.setItem(KEY, wanted);
    } catch (e) {
      // No storage: let the form go to the server, which sets the cookie.
      return;
    }
    event.preventDefault();
    writeCookie(wanted, 31536000);
    if (root.lang !== wanted) {
      location.reload();
    }
  });
})();
