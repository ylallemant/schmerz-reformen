// The map.
//
// Two kinds of map are drawn from this file. The main one — the landing page —
// asks the site what is in view every time the reader moves it, and draws
// three layers: topics, actions and collectives. The small ones illustrate a
// single place on a topic's or an action's own page.
//
// Everything a popup says arrives already worded from the server, in the
// reader's language. This script places text it was handed and never
// assembles markup from it: every node is made with createElement and filled
// with textContent, so a title containing a tag is a title that shows a tag.
(function () {
  "use strict";

  if (typeof L === "undefined") {
    return;
  }

  var TILES = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";

  // Germany, whole. Where the reforms this site is about are being made, and
  // the view from which "how widely are they spread" can be seen at all.
  var HOME = { center: [51.1657, 10.4515], zoom: 6 };

  function tiles(map, attribution) {
    L.tileLayer(TILES, { maxZoom: 19, attribution: attribution }).addTo(map);
  }

  // A marker is a colour and a shape: a topic is a diamond, an action a
  // circle, a collective a square. The shape is the part that matters — the
  // stylesheet draws it from the class — and the glyph inside says the same
  // thing a third way: an action carries the day it happens on.
  function pin(layer, glyph) {
    var holder = document.createElement("div");
    holder.className = "pin " + layer;

    var inner = document.createElement("span");
    inner.textContent = glyph || "";
    holder.appendChild(inner);

    return L.divIcon({
      html: holder,
      className: "",
      iconSize: [26, 26],
      iconAnchor: [13, 13],
      popupAnchor: [0, -14]
    });
  }

  function element(tag, className, text) {
    var node = document.createElement(tag);
    if (className) {
      node.className = className;
    }
    if (text) {
      node.textContent = text;
    }
    return node;
  }

  // What a marker says when it is opened: the same card the listings show, at
  // the width a popup allows.
  function popup(item, strings) {
    var card = element("div", "map-popup" + (item.cancelled ? " cancelled" : ""));

    var meta = element("div", "card-meta");
    meta.appendChild(element("span", "tag " + item.layer, item.tag));
    if (item.cancelled) {
      meta.appendChild(element("span", "pill alert", strings.cancelled));
    }
    card.appendChild(meta);

    if (item.figure) {
      card.appendChild(element("p", "amount", item.figure));
    }

    var title = element("h3", "card-title");
    var link = element("a", "", item.title);
    // The address is one the server built from an identifier; it is assigned
    // as a property, never concatenated into markup.
    link.href = item.href;
    title.appendChild(link);
    card.appendChild(title);

    if (item.meta) {
      card.appendChild(element("p", "card-text", item.meta));
    }
    if (item.text) {
      card.appendChild(element("p", "card-text", item.text));
    }
    return card;
  }

  // --- the main map -------------------------------------------------------

  function mainMap(holder) {
    var status = document.getElementById("map-status");
    var toggles = document.querySelectorAll("#map-layers input[type=checkbox]");
    var strings = { cancelled: holder.dataset.cancelled, failed: holder.dataset.failed };

    var map = L.map(holder, { scrollWheelZoom: false }).setView(HOME.center, HOME.zoom);
    tiles(map, holder.dataset.attribution);

    // Scrolling the page must not zoom the map out from under somebody who
    // was only passing over it; a click says they mean the map.
    map.on("click", function () { map.scrollWheelZoom.enable(); });
    map.on("mouseout", function () { map.scrollWheelZoom.disable(); });

    var cluster = L.markerClusterGroup({ showCoverageOnHover: false, maxClusterRadius: 45 });
    map.addLayer(cluster);

    var items = [];

    function enabled() {
      var on = {};
      toggles.forEach(function (toggle) {
        on[toggle.value] = toggle.checked;
      });
      return on;
    }

    function draw() {
      var on = enabled();
      cluster.clearLayers();

      items.forEach(function (item) {
        if (!on[item.layer]) {
          return;
        }
        var marker = L.marker([item.lat, item.lng], {
          icon: pin(item.layer, item.glyph),
          title: item.title,
          alt: item.tag + ": " + item.title
        });
        marker.bindPopup(popup(item, strings));
        cluster.addLayer(marker);
      });
    }

    // Each move asks again, and only the latest answer is drawn: a slow reply
    // to a view the reader has already left must not replace the one they are
    // looking at.
    var asked = 0;

    function load() {
      var bounds = map.getBounds();
      var query = [
        bounds.getNorth(), bounds.getSouth(), bounds.getEast(), bounds.getWest()
      ].map(function (edge) { return edge.toFixed(4); }).join(",");

      var mine = ++asked;
      fetch(holder.dataset.endpoint + "?bounds=" + encodeURIComponent(query), {
        headers: { Accept: "application/json" }
      })
        .then(function (response) {
          if (!response.ok) {
            throw new Error("map: " + response.status);
          }
          return response.json();
        })
        .then(function (answer) {
          if (mine !== asked) {
            return;
          }
          items = answer.items || [];
          if (status) {
            status.textContent = answer.status || "";
          }
          draw();
        })
        .catch(function () {
          if (mine === asked && status) {
            status.textContent = strings.failed;
          }
        });
    }

    // Not on every pixel of a drag: when the reader lets go.
    var waiting = null;
    map.on("moveend", function () {
      window.clearTimeout(waiting);
      waiting = window.setTimeout(load, 200);
    });

    toggles.forEach(function (toggle) {
      toggle.addEventListener("change", draw);
    });

    load();
  }

  // --- a small map of one place -------------------------------------------

  function placeMap(holder) {
    var lat = parseFloat(holder.dataset.latitude);
    var lng = parseFloat(holder.dataset.longitude);
    if (!isFinite(lat) || !isFinite(lng) || (lat === 0 && lng === 0)) {
      return;
    }

    var layer = holder.dataset.layer || "topic";
    // An action is somewhere to walk to and a topic is somewhere on a map of
    // the region: the first opens on streets, the second on the town.
    var zoom = layer === "action" ? 15 : 11;

    var map = L.map(holder, { scrollWheelZoom: false }).setView([lat, lng], zoom);
    tiles(map, holder.dataset.attribution);
    L.marker([lat, lng], { icon: pin(layer, ""), keyboard: false }).addTo(map);
  }

  var main = document.getElementById("map");
  if (main && main.dataset.endpoint) {
    mainMap(main);
  }
  document.querySelectorAll(".map.small").forEach(placeMap);
})();
