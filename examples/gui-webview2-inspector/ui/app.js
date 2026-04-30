const stateOut = document.querySelector("#state");
const detail = document.querySelector("#detail");

function renderState(state) {
  stateOut.textContent = JSON.stringify(state);
  if (state && state.detail) {
    detail.textContent = state.detail;
  }
  console.info("state rendered", state);
}

function emit(name, payload) {
  if (window.osty) {
    window.osty.emit(name, payload || {});
  }
}

document.querySelector("#refresh").addEventListener("click", () => {
  console.log("refresh requested");
  emit("refresh", {});
});

document.querySelector("#inspect").addEventListener("click", () => {
  console.warn("opening WebView2 devtools");
  emit("inspect", {});
});

document.querySelector("#quit").addEventListener("click", () => {
  console.log("quit requested");
  emit("quit", {});
});

if (window.osty) {
  window.osty.onState(renderState);
  window.osty.onCommand((name, payload) => {
    if (name === "flash") {
      detail.textContent = `Command from ${payload && payload.source ? payload.source : "Osty"}: ${payload && payload.reason ? payload.reason : "update"}`;
      console.debug("command handled", name, payload);
    }
  });
} else {
  renderState({ status: "preview", detail: "Run this example through WebView2 on Windows." });
}
