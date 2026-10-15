// A trackpad pinch zooms the map, not the page — in every browser, on every
// map of both services.
//
// The maps ignore the scroll wheel until they are clicked, so somebody
// scrolling down the page is not caught by a map passing under the pointer.
// A pinch is different: nobody pinches by accident. But it reaches the page
// in two forms Leaflet 1.9 does not treat as a deliberate zoom:
//
//   Chrome, Firefox, Edge — as wheel events with ctrlKey set. With the wheel
//     handler off, nobody claims them and the browser zooms the whole page.
//     A pinch therefore switches the map's wheel zoom on, exactly as a click
//     does; Leaflet then zooms on the rest of the gesture, and leaving the
//     map switches it off again.
//
//   Safari on a Mac — as its own gesturestart/gesturechange/gestureend
//     events, which Leaflet does not listen to at all, so the page zoomed
//     whatever the map's state. They are handled here.
//
// Touch screens are left alone: Leaflet already zooms on two fingers, and
// Safari on a phone or tablet sends the same gesture events alongside the
// touches it handles — acting on both would zoom twice.
//
// Loaded after leaflet.js and before any map is made: an init hook only runs
// for maps created after it was added.
(function () {
  "use strict";

  if (typeof L === "undefined") {
    return;
  }

  L.Map.addInitHook(function () {
    var map = this;
    var container = map.getContainer();
    var options = { passive: false };

    container.addEventListener("wheel", function (event) {
      if (!event.ctrlKey || !map.scrollWheelZoom || map.scrollWheelZoom.enabled()) {
        return;
      }
      // This first event is only kept from zooming the page; the handler
      // switched on now takes every one after it.
      event.preventDefault();
      map.scrollWheelZoom.enable();
    }, options);

    // Fingers on a screen: Leaflet's own touch zoom has them.
    var touching = false;
    container.addEventListener("touchstart", function () { touching = true; }, { passive: true });
    container.addEventListener("touchend", function (event) {
      touching = event.touches.length > 0;
    }, { passive: true });
    container.addEventListener("touchcancel", function () { touching = false; }, { passive: true });

    var pinch = null;
    container.addEventListener("gesturestart", function (event) {
      if (touching) {
        return;
      }
      event.preventDefault();
      pinch = { zoom: map.getZoom(), around: map.mouseEventToContainerPoint(event) };
    }, options);

    container.addEventListener("gesturechange", function (event) {
      if (!pinch) {
        return;
      }
      event.preventDefault();
      // scale is relative to the start of the gesture: doubling the distance
      // between the fingers is one zoom level, as it is on a touch screen.
      var zoom = Math.round(pinch.zoom + Math.log2(event.scale));
      if (zoom !== map.getZoom()) {
        map.setZoomAround(pinch.around, zoom);
      }
    }, options);

    container.addEventListener("gestureend", function (event) {
      if (!pinch) {
        return;
      }
      event.preventDefault();
      pinch = null;
    }, options);
  });
})();
