import { state } from "/static/js/state.js";
import { api, setUnauthorizedHandler } from "/static/js/api.js";
import { parseRoute } from "/static/js/router.js";
import {
  renderLogin,
  renderProjects,
  renderSessions,
} from "/static/js/screens.js";
import {
  renderTerminal,
  destroyTerminal,
  fitTerminal,
} from "/static/js/terminal.js";

setUnauthorizedHandler(() => {
  renderLogin("");
});

document.addEventListener("DOMContentLoaded", () => {
  syncViewport();
  window.addEventListener("hashchange", onHashChange);
  boot();
});

function syncViewport() {
  const viewport = window.visualViewport;
  if (!viewport) {
    return;
  }
  const apply = () => {
    document.body.style.height = `${viewport.height}px`;
    if (state.terminalSession) {
      fitTerminal();
    }
  };
  viewport.addEventListener("resize", apply);
  apply();
}

async function boot() {
  try {
    state.me = await api("/api/me");
  } catch (error) {
    if (error.status !== 401) {
      renderLogin(error.message);
      return;
    }
    state.me = null;
  }
  if (!state.me) {
    renderLogin("");
    return;
  }
  if (!location.hash) {
    location.hash = "#/projects";
    return;
  }
  renderRoute();
}

function onHashChange() {
  if (state.terminalSession && parseRoute().name !== "terminal") {
    destroyTerminal();
  }
  if (!state.me) {
    renderLogin("");
    return;
  }
  renderRoute();
}

function renderRoute() {
  const route = parseRoute();
  if (route.name === "projects") {
    renderProjects();
    return;
  }
  if (route.name === "sessions") {
    renderSessions(route.projectId);
    return;
  }
  renderTerminal(route.projectId, route.chatId);
}
