// Searching as an editor types, in two shapes:
//
// - a table narrowed in place: the search field names the table in
//   `data-filter-for`, and each row carries the text it is found by in
//   `data-search`;
// - the organisation picker (`[data-combobox]`): one text field, and a list of
//   matches that opens below it as somebody types. Underneath is the <select>
//   the form posts; this script hides it and keeps it in step.
//
// Both match the same way. Every word typed has to appear somewhere —
// "verdi düsseldorf" finds the Düsseldorf district of ver.di, not every ver.di
// and every Düsseldorf — and German is matched the way people type it on a
// keyboard without umlauts: "dusseldorf" and "duesseldorf" both find
// "Düsseldorf".
//
// Everything shown is text the template rendered and escaped into data
// attributes. This script copies it into elements it creates with
// textContent, and never parses any of it as markup.
//
// Without this script the table is whole and the picker is a plain <select>:
// longer to scroll, and nothing worse.
(function () {
  "use strict";

  // Two spellings of one text: accents dropped ("dusseldorf"), and umlauts
  // written out ("duesseldorf").
  function spellings(text) {
    var lower = (text || "").toLowerCase();
    var spelled = lower
      .replace(/ä/g, "ae").replace(/ö/g, "oe").replace(/ü/g, "ue").replace(/ß/g, "ss");
    var plain = lower.replace(/ß/g, "ss").normalize("NFD").replace(/[̀-ͯ]/g, "");
    return [plain, spelled];
  }

  function words(query) {
    return query.trim().split(/\s+/).filter(function (word) { return word !== ""; });
  }

  function matches(haystacks, query) {
    return words(query).every(function (word) {
      var needles = spellings(word);
      return haystacks.some(function (haystack) {
        return needles.some(function (needle) { return haystack.indexOf(needle) !== -1; });
      });
    });
  }

  // --- a table, narrowed in place ---

  function filterTable(search) {
    var table = document.getElementById(search.dataset.filterFor);
    var body = table && table.querySelector("tbody");
    if (!body) {
      return;
    }
    var rows = Array.prototype.slice.call(body.children).map(function (row) {
      return { element: row, haystacks: spellings(row.dataset.search || row.textContent) };
    });
    var counter = document.querySelector('[data-filter-count="' + table.id + '"]');

    search.hidden = false;
    search.addEventListener("input", function () {
      var query = search.value;
      var shown = 0;
      rows.forEach(function (row) {
        var visible = query.trim() === "" || matches(row.haystacks, query);
        row.element.hidden = !visible;
        if (visible) {
          shown++;
        }
      });
      if (counter) {
        counter.textContent = query.trim() === "" ? "" :
          (counter.dataset.text || "{shown} / {total}")
            .replace("{shown}", String(shown)).replace("{total}", String(rows.length));
      }
    });
  }

  // --- the organisation picker ---

  // How many matches the list shows at once. Enough to scroll through; a
  // query that matches more than this wants another word, not a longer list.
  var shownAtMost = 50;

  function combobox(holder) {
    var select = holder.querySelector("select");
    if (!select) {
      return;
    }
    var listID = select.id + "-matches";

    var choices = Array.prototype.slice.call(select.options)
      .filter(function (option) { return option.value !== ""; })
      .map(function (option) {
        return {
          option: option,
          name: option.dataset.name || option.textContent,
          detail: option.dataset.detail || "",
          haystacks: spellings(option.dataset.search || option.textContent)
        };
      });

    var input = document.createElement("input");
    input.type = "text";
    input.className = "combo-input";
    input.id = select.id + "-search";
    input.autocomplete = "off";
    input.placeholder = holder.dataset.placeholder || "";
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-controls", listID);

    var list = document.createElement("ul");
    list.className = "combo-list";
    list.id = listID;
    list.setAttribute("role", "listbox");
    list.hidden = true;

    // The label pointed at the <select>; it now names the field the editor
    // actually types in.
    var label = document.querySelector('label[for="' + select.id + '"]');
    if (label) {
      label.htmlFor = input.id;
    }

    select.hidden = true;
    select.tabIndex = -1;
    holder.insertBefore(input, select);
    holder.insertBefore(list, select);

    var shown = [];
    var active = -1;

    // Whether the Enter being pressed was used to choose. Set on keydown and
    // cleared on keyup, so the rest of that press — the character event, and
    // the form submission some browsers start from it even when the keydown
    // was cancelled — is swallowed too.
    var choosingWithEnter = false;

    function chosen() {
      return choices.filter(function (choice) { return choice.option.selected; })[0];
    }

    // The field shows the chosen organisation's name, or nothing.
    function showChoice() {
      var choice = chosen();
      input.value = choice ? choice.name : "";
      input.setCustomValidity("");
    }

    function close() {
      list.hidden = true;
      input.setAttribute("aria-expanded", "false");
      input.removeAttribute("aria-activedescendant");
      active = -1;
    }

    function highlight(index) {
      active = index;
      Array.prototype.forEach.call(list.children, function (item, i) {
        item.setAttribute("aria-selected", i === index ? "true" : "false");
      });
      if (index >= 0 && list.children[index]) {
        input.setAttribute("aria-activedescendant", list.children[index].id);
        list.children[index].scrollIntoView({ block: "nearest" });
      } else {
        input.removeAttribute("aria-activedescendant");
      }
    }

    function choose(choice) {
      select.value = choice ? choice.option.value : "";
      select.dispatchEvent(new Event("change", { bubbles: true }));
      showChoice();
      close();
    }

    function open(query) {
      shown = choices.filter(function (choice) { return matches(choice.haystacks, query); })
        .slice(0, shownAtMost);

      var items = shown.map(function (choice, i) {
        var item = document.createElement("li");
        item.id = listID + "-" + i;
        item.setAttribute("role", "option");
        item.setAttribute("aria-selected", "false");

        var name = document.createElement("span");
        name.className = "combo-name";
        name.textContent = choice.name;
        item.appendChild(name);
        if (choice.detail) {
          var detail = document.createElement("span");
          detail.className = "combo-detail";
          detail.textContent = choice.detail;
          item.appendChild(detail);
        }

        // Chosen on mousedown, before the field loses focus and closes the
        // list under the pointer.
        item.addEventListener("mousedown", function (event) {
          event.preventDefault();
          choose(choice);
        });
        return item;
      });
      if (items.length === 0) {
        var none = document.createElement("li");
        none.className = "combo-empty";
        none.textContent = holder.dataset.empty || "";
        items.push(none);
      }

      list.replaceChildren.apply(list, items);
      list.hidden = false;
      input.setAttribute("aria-expanded", "true");
      highlight(shown.length > 0 ? 0 : -1);
    }

    input.addEventListener("input", function () {
      // What was chosen stays chosen while somebody types: only picking from
      // the list changes it, and leaving the field puts its name back.
      input.setCustomValidity("");
      if (input.value.trim() === "") {
        close();
        return;
      }
      open(input.value);
    });

    input.addEventListener("keydown", function (event) {
      var isOpen = !list.hidden;
      switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (!isOpen) {
          open(input.value);
        } else if (shown.length > 0) {
          highlight(Math.min(active + 1, shown.length - 1));
        }
        break;
      case "ArrowUp":
        if (isOpen && shown.length > 0) {
          event.preventDefault();
          highlight(Math.max(active - 1, 0));
        }
        break;
      case "Enter":
        // With the list open, Enter chooses; it never submits the form with
        // a half-typed name in the field.
        if (isOpen) {
          event.preventDefault();
          choosingWithEnter = true;
          if (active >= 0) {
            choose(shown[active]);
          }
        }
        break;
      case "Escape":
        if (isOpen) {
          event.preventDefault();
          close();
          showChoice();
        }
        break;
      }
    });

    input.addEventListener("keypress", function (event) {
      if (choosingWithEnter && event.key === "Enter") {
        event.preventDefault();
      }
    });
    input.addEventListener("keyup", function (event) {
      if (event.key === "Enter") {
        choosingWithEnter = false;
      }
    });

    // Leaving the field puts back the name of what is chosen, so the field
    // and the form never disagree about which organisation this is — or, if
    // the field was emptied, chooses nothing.
    input.addEventListener("blur", function () {
      close();
      if (input.value.trim() === "") {
        choose(null);
        return;
      }
      showChoice();
    });

    // The <select> is hidden, so the browser cannot point at it when a
    // required choice is missing; the field it is typed into says so instead.
    var form = select.form;
    if (form) {
      form.addEventListener("submit", function (event) {
        if (choosingWithEnter) {
          event.preventDefault();
        }
      });
    }
    if (form && select.required) {
      form.addEventListener("submit", function (event) {
        if (select.value === "") {
          event.preventDefault();
          input.setCustomValidity(holder.dataset.requiredMessage || "");
          input.reportValidity();
        }
      });
      select.required = false;
    }

    showChoice();
  }

  Array.prototype.forEach.call(document.querySelectorAll("[data-filter-for]"), filterTable);
  Array.prototype.forEach.call(document.querySelectorAll("[data-combobox]"), combobox);
})();
