const stateOut = document.querySelector("#state");
const detail = document.querySelector("#detail");

function renderState(state) {
  stateOut.textContent = JSON.stringify(state);
  if (state && state.detail) {
    detail.textContent = state.detail;
  }
}

function emit(name, payload) {
  if (window.osty) {
    window.osty.emit(name, payload || {});
  }
}

document.querySelector("#refresh").addEventListener("click", () => {
  emit("refresh", {});
});

document.querySelector("#inspect").addEventListener("click", () => {
  emit("inspect", {});
});

document.querySelector("#quit").addEventListener("click", () => {
  emit("quit", {});
});

if (window.osty) {
  window.osty.onState(renderState);
} else {
  renderState({ status: "preview", detail: "Run this example through WebView2 on Windows." });
}
