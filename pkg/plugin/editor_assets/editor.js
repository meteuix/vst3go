const snapshot = JSON.parse(atob("__VST3GO_SNAPSHOT__"));
const model = snapshot.model;
const sectionsRoot = document.getElementById("sections");
const statusText = document.getElementById("snapshot-status");
const saveButton = document.getElementById("save-snapshot");
const restoreButton = document.getElementById("restore-snapshot");
const storageKey = "vst3go.editor.snapshot." + (model.plugin.id || "default");
document.getElementById("plugin-vendor").textContent = model.plugin.vendor || "VST3Go";
document.getElementById("plugin-name").textContent = model.plugin.name || "Plugin Editor";
document.getElementById("plugin-meta").textContent = [model.plugin.category, model.plugin.version].filter(Boolean).join(" · ");

// post sends a message to the plugin: WKWebView/WebKitGTK script message
// handler or WebView2.
function post(message) {
  if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.vst3go) {
    window.webkit.messageHandlers.vst3go.postMessage(message);
    return;
  }

  if (window.chrome && window.chrome.webview) {
    window.chrome.webview.postMessage(message);
  }
}

function sendChange(id, value) {
  post({ type: "param-change", id, value });
}

// bindGesture reports a pointer drag on element as one edit gesture, so the
// host records it as one automation pass and undo step.
function bindGesture(element, id) {
  let active = false;
  const end = () => {
    if (active) {
      active = false;
      post({ type: "end-edit", id });
    }
  };
  element.addEventListener("pointerdown", () => {
    if (!active) {
      active = true;
      post({ type: "begin-edit", id });
    }
  });
  element.addEventListener("pointerup", end);
  element.addEventListener("pointercancel", end);
  element.addEventListener("lostpointercapture", end);
}

const controlBindings = new Map();
const controlIndex = new Map();

function setStatus(message) {
  if (statusText) {
    statusText.textContent = message;
  }
}

function updateSnapshotControl(control, normalized, plain) {
  control.normalized = normalized;
  control.plain = plain;
}

function updateBoundControl(binding, control, normalized, plain) {
  if (binding.select) {
    const selected = binding.steps > 0 ? Math.round(normalized * binding.steps) / binding.steps : normalized;
    binding.select.value = String(selected);
  }
  if (binding.range) {
    binding.range.value = String(normalized);
  }
  if (binding.value) {
    binding.value.value = String(plain);
  }
  if (binding.readout) {
    binding.readout.textContent = plain.toFixed(3);
  }
  if (binding.card) {
    binding.card.dataset.controlState = control.readOnly ? "read-only" : "live";
  }
}

function persistSnapshot() {
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(snapshot));
    if (restoreButton) {
      restoreButton.disabled = false;
    }
    setStatus("Snapshot saved locally");
  } catch (error) {
    setStatus("Snapshot save failed");
  }
}

function loadSavedSnapshot() {
  try {
    const saved = window.localStorage.getItem(storageKey);
    if (!saved) {
      setStatus("No saved snapshot yet");
      return null;
    }

    const parsed = JSON.parse(saved);
    if (!parsed || !parsed.model || !Array.isArray(parsed.model.sections)) {
      setStatus("Saved snapshot is invalid");
      return null;
    }

    return parsed;
  } catch (error) {
    setStatus("Snapshot restore failed");
    return null;
  }
}

function updateControl(control, normalized, plain, notifyGo) {
  updateSnapshotControl(control, normalized, plain);

  const binding = controlBindings.get(control.id);
  if (binding) {
    updateBoundControl(binding, control, normalized, plain);
  }

  if (notifyGo) {
    sendChange(control.id, normalized);
  }
}

function findControl(controlId) {
  return controlIndex.get(controlId) || null;
}

function applySnapshot(nextSnapshot, notifyGo) {
  if (!nextSnapshot || !nextSnapshot.model || !Array.isArray(nextSnapshot.model.sections)) {
    return false;
  }

  nextSnapshot.model.sections.forEach((section) => {
    section.controls.forEach((control) => {
      const existing = findControl(control.id);
      if (!existing) {
        return;
      }

      updateControl(existing, control.normalized, control.plain, notifyGo);
    });
  });

  setStatus(notifyGo ? "Snapshot restored" : "Snapshot loaded");
  return true;
}

window.__vst3goUpdateParameter = function(id, normalized, plain) {
  const control = findControl(id);
  if (!control) {
    return;
  }

  updateControl(control, normalized, plain, false);
  persistSnapshot();
};

function restoreFromLocalStorage() {
  const saved = loadSavedSnapshot();
  if (!saved) {
    return;
  }

  applySnapshot(saved, true);
}

function renderControl(control) {
  if (control.hidden) {
    return null;
  }

  const card = document.createElement("article");
  card.className = "card";
  if (control.readOnly) {
    card.classList.add("card--readonly");
    card.dataset.controlState = "read-only";
  } else {
    card.dataset.controlState = "live";
  }
  card.innerHTML = "<p class=\"card__label\">" + (control.shortName || control.name) + "</p>" +
    "<h3>" + control.name + "</h3>" +
    "<p>" + (control.unit || control.kind) + "</p>";

  const field = document.createElement("div");
  field.className = "control";

  if (control.kind === "choice") {
    const select = document.createElement("select");
    const steps = Math.max(control.stepCount || 1, 1);
    for (let index = 0; index <= steps; index += 1) {
      const option = document.createElement("option");
      option.value = String(index / steps);
      option.textContent = control.stepCount > 0 ? String(index) : (index / steps).toFixed(2);
      select.appendChild(option);
    }
    const selected = steps > 0 ? Math.round(control.normalized * steps) / steps : control.normalized;
    select.value = String(selected);
    select.disabled = control.readOnly;
    select.addEventListener("change", () => {
      if (control.readOnly) {
        return;
      }
      const normalized = Number(select.value);
      const plain = control.min + normalized * (control.max - control.min);
      updateControl(control, normalized, plain, true);
      persistSnapshot();
    });
    field.appendChild(select);
    controlBindings.set(control.id, { select, readout: null, steps });
  } else {
    const range = document.createElement("input");
    range.type = "range";
    range.min = "0";
    range.max = "1";
    range.step = control.kind === "toggle" ? "1" : "0.001";
    range.value = String(control.normalized);
    range.disabled = control.readOnly;
    const value = document.createElement("input");
    value.type = "number";
    value.min = String(control.min);
    value.max = String(control.max);
    value.step = control.kind === "toggle" ? "1" : "0.001";
    value.value = String(control.plain);
    value.disabled = control.readOnly;

    function updateFromNormalized(normalized) {
      if (control.readOnly) {
        return;
      }
      const clamped = Math.max(0, Math.min(1, normalized));
      const plain = control.min + clamped * (control.max - control.min);
      updateControl(control, clamped, plain, true);
      persistSnapshot();
    }

    range.addEventListener("input", () => updateFromNormalized(Number(range.value)));
    bindGesture(range, control.id);
    value.addEventListener("change", () => {
      const plain = Number(value.value);
      const normalized = control.max > control.min ? (plain - control.min) / (control.max - control.min) : 0;
      updateFromNormalized(normalized);
    });

    field.appendChild(range);
    field.appendChild(value);
    controlBindings.set(control.id, { range, value, readout: null });
  }

  const readout = document.createElement("div");
  readout.className = "control__meta";
  readout.innerHTML = "<span>" + control.kind + "</span>";
  const valueReadout = document.createElement("span");
  valueReadout.className = "control__value";
  valueReadout.textContent = control.readOnly ? "locked" : Number(control.plain).toFixed(3);
  readout.appendChild(valueReadout);
  if (control.readOnly) {
    const readOnlyBadge = document.createElement("span");
    readOnlyBadge.className = "control__badge";
    readOnlyBadge.textContent = "Read-only";
    readout.appendChild(readOnlyBadge);
  }
  card.appendChild(field);
  card.appendChild(readout);

  controlBindings.get(control.id).card = card;
  controlBindings.get(control.id).readout = valueReadout;
  controlIndex.set(control.id, control);
  return card;
}

model.sections.forEach((section) => {
  const wrapper = document.createElement("section");
  wrapper.className = "card";
  const heading = document.createElement("h2");
  heading.textContent = section.title;
  wrapper.appendChild(heading);
  const grid = document.createElement("div");
  grid.className = "grid";
  section.controls.forEach((control) => {
    controlIndex.set(control.id, control);
    const card = renderControl(control);
    if (card) {
      grid.appendChild(card);
    }
  });
  wrapper.appendChild(grid);
  sectionsRoot.appendChild(wrapper);
});

if (saveButton) {
  saveButton.addEventListener("click", () => persistSnapshot());
}

if (restoreButton) {
  restoreButton.addEventListener("click", () => restoreFromLocalStorage());
  let hasSavedSnapshot = false;
  try {
    hasSavedSnapshot = !!window.localStorage.getItem(storageKey);
  } catch (error) {
    hasSavedSnapshot = false;
  }
  restoreButton.disabled = !hasSavedSnapshot;
}

if (window.chrome && window.chrome.webview) {
  window.chrome.webview.addEventListener("message", (event) => {
    const payload = typeof event.data === "string" ? JSON.parse(event.data) : event.data;
    if (!payload || payload.type !== "param-change") {
      return;
    }

    const control = findControl(payload.id);
    if (!control) {
      return;
    }

    updateControl(control, Number(payload.normalized), Number(payload.plain), false);
    persistSnapshot();
  });
}
