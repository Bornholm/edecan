// app.js — JavaScript applicatif d'edecán, embarqué dans le binaire
// (cf. internal/http/static/embed.go) et chargé après htmx (voir base.templ).
//
// Tout le code est rangé sous le namespace `window.edc` et organisé en modules
// (stream, copy, share, autoscroll…) branchés à l'initialisation. Les
// gestionnaires d'événements utilisent la délégation sur `document` afin de
// rester valides après les remplacements de DOM opérés par htmx (un listener
// posé sur un nœud remplacé serait perdu au premier swap).
(function () {
  "use strict";

  window.edc = window.edc || {};

  // register enregistre un module nommé et l'initialise une fois le DOM prêt.
  // Chaque module expose au minimum une fonction init() idempotente.
  const modules = [];
  window.edc.register = function (name, module) {
    window.edc[name] = module;
    modules.push(module);
  };

  function init() {
    for (const module of modules) {
      if (typeof module.init === "function") {
        module.init();
      }
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
