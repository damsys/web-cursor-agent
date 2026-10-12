export function parseRoute() {
  const parts = location.hash.replace(/^#/, "").split("/").filter(Boolean);
  if (parts[0] !== "projects") {
    return { name: "projects" };
  }
  if (parts.length === 1) {
    return { name: "projects" };
  }
  const projectId = decodeURIComponent(parts[1]);
  if (parts[2] === "terminal") {
    return {
      name: "terminal",
      projectId,
      chatId: parts[3] ? decodeURIComponent(parts[3]) : "",
    };
  }
  return { name: "sessions", projectId };
}

export function isCurrentTerminalRoute(projectId, chatId) {
  const route = parseRoute();
  return (
    route.name === "terminal" &&
    route.projectId === projectId &&
    (route.chatId || "") === (chatId || "")
  );
}

export function isCurrentRoute(name, projectId = "") {
  const route = parseRoute();
  if (route.name !== name) {
    return false;
  }
  if (name === "sessions") {
    return route.projectId === projectId;
  }
  return true;
}
