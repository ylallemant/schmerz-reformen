// The place picker: a map an editor aims a pin on.
//
// It fills three hidden fields — latitude, longitude and the zoom the pin was
// dropped at — and nothing else. The backend does the rest: it stores the pin
// exactly where it was put, and reads the zoom as a statement of how precise
// the editor meant to be, which decides how much of an address the place is
// named with.
//
// Without this script the fields keep the values they were rendered with, so
// a save leaves the place where it was.
(function () {
  "use strict";

  var holder = document.getElementById("picker");
  if (!holder || typeof L === "undefined") {
    return;
  }

  var latitude = document.getElementById("latitude");
  var longitude = document.getElementById("longitude");
  var zoom = document.getElementById("zoom");
  var place = document.getElementById("place");
  var status = document.getElementById("picker-status");
  var clear = document.getElementById("picker-clear");

  var startLat = parseFloat(holder.dataset.latitude) || 0;
  var startLng = parseFloat(holder.dataset.longitude) || 0;
  var startZoom = parseInt(holder.dataset.zoom, 10) || 0;
  var pinned = startLat !== 0 || startLng !== 0;

  // What the name field held when the page was drawn. If it still holds that
  // when the pin moves, the name described the old place and is emptied, so
  // the backend names the new one; anything the editor typed since is theirs
  // and is left alone.
  var originalPlace = place ? place.value : "";

  // With nothing pinned the map opens on Germany, at a zoom where a click
  // would name a region rather than a point. That is on purpose: the first
  // thing an editor does is zoom to where they mean, and the zoom they stop
  // at is the answer to "how precise".
  var map = L.map(holder, { scrollWheelZoom: false }).setView(
    pinned ? [startLat, startLng] : [51.1657, 10.4515],
    pinned ? (startZoom || 13) : 6
  );

  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: holder.dataset.attribution
  }).addTo(map);

  // Scrolling the page must not zoom the map out from under somebody who was
  // only passing over it; a click says they mean the map.
  map.on("click", function () { map.scrollWheelZoom.enable(); });
  map.on("mouseout", function () { map.scrollWheelZoom.disable(); });

  var marker = null;
  if (pinned) {
    marker = L.marker([startLat, startLng]).addTo(map);
  }

  function say(text) {
    if (status) {
      status.textContent = text;
    }
  }

  map.on("click", function (event) {
    if (marker) {
      marker.setLatLng(event.latlng);
    } else {
      marker = L.marker(event.latlng).addTo(map);
    }

    latitude.value = event.latlng.lat.toFixed(6);
    longitude.value = event.latlng.lng.toFixed(6);
    zoom.value = String(map.getZoom());

    if (place && place.value === originalPlace) {
      place.value = "";
    }
    say(holder.dataset.moved);
  });

  if (clear) {
    clear.addEventListener("click", function () {
      if (marker) {
        map.removeLayer(marker);
        marker = null;
      }
      latitude.value = "0";
      longitude.value = "0";
      zoom.value = "0";
      say(holder.dataset.cleared);
    });
  }
})();
