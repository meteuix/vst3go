const snapshot = JSON.parse(atob("__VST3GO_SNAPSHOT__"));
const model = snapshot.model;
const sectionsRoot = document.getElementById("sections");
const presetList = document.getElementById("preset-list");
const presetName = document.getElementById("preset-name");
const presetSave = document.getElementById("preset-save");
const presetStatus = document.getElementById("preset-status");
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
    binding.readout.textContent = control.readOnly ? "locked" : plain.toFixed(3);
  }
}

function updateControl(control, normalized, plain, notifyGo) {
  control.normalized = normalized;
  control.plain = plain;

  const binding = controlBindings.get(control.id);
  if (binding) {
    updateBoundControl(binding, control, normalized, plain);
  }

  if (notifyGo) {
    sendChange(control.id, normalized);
  }
}

// Presets: the plugin's user preset folder, as .vstpreset files that hosts
// list too.
function showPresets(message) {
  presetList.textContent = "";
  const placeholder = document.createElement("option");
  placeholder.value = "";
  placeholder.textContent = message.presets && message.presets.length ? "Presets…" : "No presets yet";
  presetList.appendChild(placeholder);
  (message.presets || []).forEach((name) => {
    const option = document.createElement("option");
    option.value = name;
    option.textContent = name;
    presetList.appendChild(option);
  });
  presetList.value = message.current || "";
  if (message.current) {
    presetName.value = message.current;
  }
  presetStatus.textContent = message.error ? message.error : (message.current || "");
  presetStatus.classList.toggle("pill--error", !!message.error);
}

presetList.addEventListener("change", () => {
  if (presetList.value) {
    post({ type: "preset-load", name: presetList.value });
  }
});
presetSave.addEventListener("click", () => {
  const name = presetName.value.trim();
  if (name) {
    post({ type: "preset-save", name });
  }
});

// __vst3goReceive handles messages from the plugin.
window.__vst3goReceive = function(message) {
  if (!message) {
    return;
  }
  if (message.type === "param-change") {
    const control = controlIndex.get(message.id);
    if (control) {
      updateControl(control, Number(message.normalized), Number(message.plain), false);
    }
  } else if (message.type === "presets") {
    showPresets(message);
  }
};

if (window.chrome && window.chrome.webview) {
  window.chrome.webview.addEventListener("message", (event) => {
    window.__vst3goReceive(typeof event.data === "string" ? JSON.parse(event.data) : event.data);
  });
}

function renderControl(control) {
  if (control.hidden) {
    return null;
  }

  const card = document.createElement("article");
  card.className = "card";
  if (control.readOnly) {
    card.classList.add("card--readonly");
  }
  card.dataset.controlState = control.readOnly ? "read-only" : "live";
  const label = document.createElement("p");
  label.className = "card__label";
  label.textContent = control.shortName || control.name;
  const title = document.createElement("h3");
  title.textContent = control.name;
  const unit = document.createElement("p");
  unit.textContent = control.unit || control.kind;
  card.append(label, title, unit);

  const field = document.createElement("div");
  field.className = "control";
  let binding;

  if (control.kind === "choice") {
    const select = document.createElement("select");
    const steps = Math.max(control.stepCount || 1, 1);
    for (let index = 0; index <= steps; index += 1) {
      const option = document.createElement("option");
      option.value = String(index / steps);
      option.textContent = control.stepCount > 0 ? String(index) : (index / steps).toFixed(2);
      select.appendChild(option);
    }
    select.value = String(Math.round(control.normalized * steps) / steps);
    select.disabled = control.readOnly;
    select.addEventListener("change", () => {
      const normalized = Number(select.value);
      updateControl(control, normalized, control.min + normalized * (control.max - control.min), true);
    });
    field.appendChild(select);
    binding = { select, steps };
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

    const updateFromNormalized = (normalized) => {
      const clamped = Math.max(0, Math.min(1, normalized));
      updateControl(control, clamped, control.min + clamped * (control.max - control.min), true);
    };
    range.addEventListener("input", () => updateFromNormalized(Number(range.value)));
    bindGesture(range, control.id);
    value.addEventListener("change", () => {
      const span = control.max - control.min;
      updateFromNormalized(span > 0 ? (Number(value.value) - control.min) / span : 0);
    });

    field.append(range, value);
    binding = { range, value };
  }

  const meta = document.createElement("div");
  meta.className = "control__meta";
  const kind = document.createElement("span");
  kind.textContent = control.kind;
  const readout = document.createElement("span");
  readout.className = "control__value";
  readout.textContent = control.readOnly ? "locked" : Number(control.plain).toFixed(3);
  meta.append(kind, readout);
  if (control.readOnly) {
    const badge = document.createElement("span");
    badge.className = "control__badge";
    badge.textContent = "Read-only";
    meta.appendChild(badge);
  }
  card.append(field, meta);

  binding.readout = readout;
  controlBindings.set(control.id, binding);
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

post({ type: "preset-list" });
