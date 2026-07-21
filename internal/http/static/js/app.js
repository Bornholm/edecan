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

  function scheduleInit() {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", init);
    } else {
      init();
    }
  }

  // Chargé en `defer`, ce bloc s'exécute avant les IIFE des modules situés plus
  // bas dans le fichier — `modules` est donc encore vide ici. On diffère
  // l'amorçage en microtâche : elle s'exécute après l'enregistrement
  // synchrone de tous les modules, sans quoi aucun ne serait initialisé.
  Promise.resolve().then(scheduleInit);
})();

// ── Module stream : chronomètre d'appel d'outil ──────────────────────────────
//
// Quand la zone de statut affiche un appel d'outil en cours (fragment portant
// data-edc-timer, cf. page.AssistantStreamStatus), on incrémente localement un
// compteur « · depuis N s » dans data-edc-elapsed. Cela prouve visuellement que
// la génération avance même pendant un long appel d'outil, sans dépendre du
// réseau (le serveur, lui, n'émet que des keep-alive pendant ce temps mort).
//
// Le chrono est piloté par htmx:afterSwap : l'extension SSE remplace le contenu
// de la zone de statut à chaque event « status » via api.swap, qui déclenche
// cet événement. Un statut sans data-edc-timer (réflexion, rédaction, analyse)
// arrête le chrono.
(function () {
  "use strict";

  let timerId = null;

  function stop() {
    if (timerId !== null) {
      window.clearInterval(timerId);
      timerId = null;
    }
  }

  function start(elapsedEl) {
    stop();
    const started = Date.now();
    const tick = function () {
      const s = Math.floor((Date.now() - started) / 1000);
      elapsedEl.textContent = " · depuis " + s + " s";
    };
    tick();
    timerId = window.setInterval(tick, 1000);
  }

  function onAfterSwap(evt) {
    const target = evt.target;
    if (
      !target ||
      !target.classList ||
      !target.classList.contains("edc-stream-status")
    ) {
      return;
    }
    const elapsed = target.querySelector("[data-edc-timer] [data-edc-elapsed]");
    if (elapsed) {
      start(elapsed);
    } else {
      stop();
    }
  }

  window.edc.register("stream", {
    init: function () {
      document.body.addEventListener("htmx:afterSwap", onAfterSwap);
      // Fin de flux (event SSE « done ») : couper tout chrono résiduel.
      document.body.addEventListener("htmx:sseClose", stop);
    },
  });
})();

// ── Module composer : blocage pendant la génération ──────────────────────────
//
// Un seul flux de génération peut être actif par session côté UI. On désactive
// donc le composer (zone de saisie + bouton d'envoi) dès l'envoi d'un message
// ou l'ouverture d'un flux SSE, et on le rouvre à la fermeture du flux (succès
// comme erreur) — ce qui empêche tout double envoi.
(function () {
  "use strict";

  function composer() {
    return document.querySelector(".edc-chat__composer");
  }

  function lock() {
    const form = composer();
    if (!form) {
      return;
    }
    const input = form.querySelector(".edc-chat__composer-input");
    const send = form.querySelector(".edc-chat__composer-send");
    if (input) {
      if (input.dataset.edcPlaceholder === undefined) {
        input.dataset.edcPlaceholder = input.getAttribute("placeholder") || "";
      }
      input.setAttribute("placeholder", "L'agent répond…");
      input.disabled = true;
    }
    if (send) {
      send.disabled = true;
    }
    form.classList.add("edc-chat__composer--busy");
  }

  function unlock() {
    const form = composer();
    if (!form) {
      return;
    }
    const input = form.querySelector(".edc-chat__composer-input");
    const send = form.querySelector(".edc-chat__composer-send");
    if (input) {
      input.disabled = false;
      if (input.dataset.edcPlaceholder !== undefined) {
        input.setAttribute("placeholder", input.dataset.edcPlaceholder);
      }
    }
    if (send) {
      send.disabled = false;
    }
    form.classList.remove("edc-chat__composer--busy");
  }

  function isComposer(el) {
    return el && el.matches && el.matches(".edc-chat__composer");
  }

  window.edc.register("composer", {
    init: function () {
      // Envoi du message : blocage immédiat (avant même l'ouverture du flux).
      document.body.addEventListener("htmx:beforeRequest", function (evt) {
        if (isComposer(evt.target)) {
          lock();
        }
      });
      // Échec de l'envoi lui-même (réseau, 4xx/5xx) : rien ne streamera, on
      // rouvre le composer.
      document.body.addEventListener("htmx:afterRequest", function (evt) {
        if (isComposer(evt.target) && evt.detail && !evt.detail.successful) {
          unlock();
        }
      });
      // Ouverture d'un flux (message initial ou « Réessayer ») : (re)blocage.
      document.body.addEventListener("htmx:sseOpen", lock);
      // Fin de flux (« done » succès ou erreur) : réouverture.
      document.body.addEventListener("htmx:sseClose", unlock);
      // Abandon après échecs de reconnexion (cf. module reconnect).
      document.body.addEventListener("edc:stream-abort", unlock);
    },
  });
})();

// ── Module autoscroll : suivi intelligent du bas de conversation ─────────────
//
// Tant que l'utilisateur est au bas de la conversation, on colle au dernier
// message à chaque nouveau contenu (streaming). S'il a remonté l'historique, on
// ne force pas le défilement : un bouton flottant « Nouveaux messages » lui
// permet de revenir en bas à la demande.
(function () {
  "use strict";

  const THRESHOLD = 80; // px de tolérance pour considérer « en bas »
  let scroller = null;
  let button = null;
  // stick mémorise l'intention de l'utilisateur : suivre le bas de la
  // conversation. Il est mis à jour sur ses défilements, JAMAIS recalculé après
  // une mutation — car l'ajout d'un token éloigne le bas et ferait croire à
  // tort qu'il a remonté l'historique.
  let stick = true;

  function atBottom() {
    return (
      scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <
      THRESHOLD
    );
  }

  function toBottom() {
    scroller.scrollTop = scroller.scrollHeight;
  }

  function showButton(show) {
    if (button) {
      button.classList.toggle("edc-scroll-bottom--visible", show);
    }
  }

  function onMutation() {
    if (stick) {
      toBottom();
      showButton(false);
    } else {
      showButton(true);
    }
  }

  window.edc.register("autoscroll", {
    init: function () {
      scroller = document.querySelector(".edc-chat__messages");
      const messages = document.getElementById("messages");
      if (!scroller || !messages) {
        return;
      }

      button = document.createElement("button");
      button.type = "button";
      button.className = "edc-scroll-bottom";
      button.setAttribute("aria-label", "Aller au dernier message");
      button.innerHTML =
        '<svg width="14" height="14" viewBox="0 0 24 24" fill="none"' +
        ' stroke="currentColor" stroke-width="2" stroke-linecap="round"' +
        ' stroke-linejoin="round" aria-hidden="true">' +
        '<path d="M12 5v14M19 12l-7 7-7-7"></path></svg>' +
        "<span>Nouveaux messages</span>";
      button.addEventListener("click", function () {
        stick = true;
        toBottom();
        showButton(false);
      });
      scroller.appendChild(button);

      // Au chargement : afficher d'emblée le dernier message.
      toBottom();

      new MutationObserver(onMutation).observe(messages, {
        childList: true,
        subtree: true,
        characterData: true,
      });

      // Le défilement de l'utilisateur (re)définit l'intention : collé au bas =
      // on suit, remonté = on s'arrête. Un scroll programmatique (toBottom) se
      // termine en bas, donc préserve stick=true.
      scroller.addEventListener("scroll", function () {
        stick = atBottom();
        if (stick) {
          showButton(false);
        }
      });
    },
  });
})();

// ── Module copy : copier un message au format Markdown ───────────────────────
//
// Chaque message expose sa source Markdown brute dans un <template> non rendu
// (cf. component.ChatMessage). Le bouton « Copier » place cette source dans le
// presse-papier — l'utilisateur récupère le Markdown d'origine (titres, listes,
// tables GFM), pas le HTML rendu.
(function () {
  "use strict";

  // copyText copie via l'API Clipboard (contexte sécurisé : HTTPS/localhost),
  // avec repli sur une textarea temporaire + execCommand sinon.
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "");
      ta.style.position = "absolute";
      ta.style.left = "-9999px";
      document.body.appendChild(ta);
      ta.select();
      let ok = false;
      try {
        ok = document.execCommand("copy");
      } catch (e) {
        ok = false;
      }
      document.body.removeChild(ta);
      if (ok) {
        resolve();
      } else {
        reject(new Error("copie impossible"));
      }
    });
  }

  // feedback affiche brièvement le résultat dans le libellé du bouton, puis
  // restaure le texte d'origine.
  function feedback(btn, text) {
    const label = btn.querySelector(".edc-message__action-text");
    if (!label) {
      return;
    }
    if (btn._edcRevert) {
      window.clearTimeout(btn._edcRevert);
    } else {
      btn.dataset.edcLabel = label.textContent;
    }
    label.textContent = text;
    btn.classList.add("edc-message__action--done");
    btn._edcRevert = window.setTimeout(function () {
      label.textContent = btn.dataset.edcLabel || "Copier";
      btn.classList.remove("edc-message__action--done");
      btn._edcRevert = null;
    }, 2000);
  }

  window.edc.register("copy", {
    init: function () {
      document.body.addEventListener("click", function (evt) {
        const btn = evt.target.closest("[data-edc-copy]");
        if (!btn) {
          return;
        }
        const msg = btn.closest(".edc-message");
        const tpl = msg && msg.querySelector(".edc-message__source");
        if (!tpl) {
          return;
        }
        // Le navigateur décode le contenu échappé du <template> ; textContent
        // restitue donc le Markdown d'origine.
        const source = tpl.content ? tpl.content.textContent : tpl.textContent;
        copyText(source).then(
          function () {
            feedback(btn, "Copié !");
          },
          function () {
            feedback(btn, "Échec");
          }
        );
      });
    },
  });
})();

// ── Module reconnect : coupure & reconnexion du flux SSE ─────────────────────
//
// L'extension SSE d'htmx tente une reconnexion automatique en cas de coupure.
// On affiche pendant ce temps un bandeau discret ; à la reprise (htmx:sseOpen)
// il disparaît. Après plusieurs échecs consécutifs, on abandonne la
// reconnexion et on remplace le flux par un encart d'erreur actionnable
// « Réessayer » (même contrat que l'erreur serveur de la Phase 1.4).
(function () {
  "use strict";

  const MAX_RETRIES = 5;
  const failures = new WeakMap();

  function container(evt) {
    const t = evt.target;
    return t && t.closest ? t.closest("[sse-connect]") : null;
  }

  function banner(c) {
    let b = c.querySelector(".edc-stream-reconnect");
    if (!b) {
      b = document.createElement("div");
      b.className = "edc-stream-reconnect";
      b.setAttribute("role", "status");
      b.textContent = "Connexion interrompue, reconnexion…";
      c.prepend(b);
    }
  }

  function clearBanner(c) {
    const b = c.querySelector(".edc-stream-reconnect");
    if (b) {
      b.remove();
    }
  }

  function giveUp(evt, c) {
    if (evt.detail && evt.detail.source) {
      evt.detail.source.close();
    }
    const url = c.getAttribute("sse-connect") || "";
    const retry = url.replace(/\/stream$/, "/retry");
    // Construction par API DOM (setAttribute n'interprète pas de HTML) plutôt
    // qu'innerHTML interpolé : l'URL de retry, bien que dérivée d'un attribut
    // posé par le serveur, ne transite jamais par une chaîne HTML.
    const card = document.createElement("div");
    card.className = "edc-stream-error";
    card.setAttribute("role", "alert");
    const body = document.createElement("div");
    body.className = "edc-stream-error__body";
    const msg = document.createElement("div");
    msg.className = "edc-stream-error__msg";
    msg.textContent =
      "La connexion au serveur a été perdue. Veuillez réessayer.";
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "edc-btn edc-btn--secondary edc-btn--sm";
    btn.textContent = "Réessayer";
    btn.setAttribute("hx-post", retry);
    btn.setAttribute("hx-target", "#messages");
    btn.setAttribute("hx-swap", "beforeend");
    body.appendChild(msg);
    body.appendChild(btn);
    card.appendChild(body);
    // Remplacer tout le conteneur SSE (et donc son sse-connect) évite qu'htmx
    // ne relance aussitôt une connexion en re-traitant l'élément.
    c.replaceWith(card);
    if (window.htmx) {
      window.htmx.process(card);
    }
    document.body.dispatchEvent(new CustomEvent("edc:stream-abort"));
  }

  window.edc.register("reconnect", {
    init: function () {
      document.body.addEventListener("htmx:sseError", function (evt) {
        const c = container(evt);
        if (!c) {
          return;
        }
        const n = (failures.get(c) || 0) + 1;
        failures.set(c, n);
        if (n < MAX_RETRIES) {
          banner(c);
        } else {
          giveUp(evt, c);
        }
      });
      document.body.addEventListener("htmx:sseOpen", function (evt) {
        const c = container(evt);
        if (!c) {
          return;
        }
        failures.set(c, 0);
        clearBanner(c);
      });
    },
  });
})();
