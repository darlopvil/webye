// @license magnet:?xt=urn:btih:0b31508aeb0634b347b8270c7bee4d411b5d4109&dn=agpl-3.0.txt AGPL-3.0
//
// webye: opens the store stock panel of a product page without reloading
// it. Without this script the same links reload the page with &stores=1
// and the server draws the panel, so nothing depends on it. It only talks
// to webye itself, keeps nothing and sets nothing.
(function () {
  "use strict";

  var enlace = document.getElementById("ver-tiendas");
  var panel = document.getElementById("tiendas");
  if (!enlace || !panel || !window.fetch || !window.history.pushState) {
    return;
  }
  var cuerpo = panel.querySelector(".panel-cuerpo");
  var cerrar = panel.querySelector(".panel-cerrar");
  var cargado = !panel.hidden;

  function cargar() {
    if (cargado) {
      return;
    }
    cuerpo.textContent = "Consultando las tiendas…";
    fetch(enlace.getAttribute("data-tiendas"), { credentials: "omit" })
      .then(function (r) {
        cargado = r.ok;
        return r.text();
      })
      .then(function (html) {
        // Drawn and escaped by webye's own templates.
        cuerpo.innerHTML = html;
      })
      .catch(function () {
        cuerpo.textContent = "No se ha podido consultar el stock en tiendas.";
      });
  }

  function abrir() {
    panel.hidden = false;
    cargar();
    cerrar.focus({ preventScroll: true });
  }

  function ocultar() {
    panel.hidden = true;
    enlace.focus({ preventScroll: true });
  }

  function cerrarPanel(e) {
    e.preventDefault();
    if (window.history.state && window.history.state.tiendas) {
      // Opened by this script: going back closes it and keeps history clean.
      window.history.back();
    } else {
      window.history.replaceState(null, "", cerrar.href);
      ocultar();
    }
  }

  enlace.addEventListener("click", function (e) {
    e.preventDefault();
    // Going back to close the panel must not scroll the page to where it
    // was when the panel opened: the visitor may have scrolled since.
    window.history.scrollRestoration = "manual";
    window.history.pushState({ tiendas: true }, "", enlace.href);
    abrir();
  });
  cerrar.addEventListener("click", cerrarPanel);
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && !panel.hidden) {
      cerrarPanel(e);
    }
  });
  window.addEventListener("popstate", function () {
    if (new URLSearchParams(window.location.search).get("stores") === "1") {
      abrir();
    } else {
      ocultar();
      // Back on the plain product page: other navigations restore the
      // scroll as usual again.
      window.setTimeout(function () {
        window.history.scrollRestoration = "auto";
      }, 0);
    }
  });
})();
// @license-end
