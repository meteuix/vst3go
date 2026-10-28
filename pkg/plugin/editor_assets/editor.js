// The built-in editor: one control per visible parameter and a preset bar,
// written against the vst3go SDK like a custom editor would be.
const sectionsRoot = document.getElementById("sections");
const presetList = document.getElementById("preset-list");
const presetName = document.getElementById("preset-name");
const presetSave = document.getElementById("preset-save");
const presetStatus = document.getElementById("preset-status");
document.getElementById("plugin-vendor").textContent = vst3go.plugin.vendor || "VST3Go";
document.getElementById("plugin-name").textContent = vst3go.plugin.name || "Plugin Editor";
document.getElementById("plugin-meta").textContent = [vst3go.plugin.category, vst3go.plugin.version].filter(Boolean).join(" · ");

function displayText(param) {
  if (param.readOnly && !param.text) {
    return "locked";
  }
  const text = param.text || param.plain.toFixed(3);
  return param.unit && !text.endsWith(param.unit) ? `${text} ${param.unit}` : text;
}

// Presets: the plugin's user preset folder, as .vstpreset files that hosts
// list too.
vst3go.presets.onChange((presets) => {
  presetList.textContent = "";
  const placeholder = document.createElement("option");
  placeholder.value = "";
  placeholder.textContent = presets.list.length ? "Presets…" : "No presets yet";
  presetList.appendChild(placeholder);
  presets.list.forEach((name) => {
    const option = document.createElement("option");
    option.value = name;
    option.textContent = name;
    presetList.appendChild(option);
  });
  presetList.value = presets.current;
  if (presets.current) {
    presetName.value = presets.current;
  }
  presetStatus.textContent = presets.error || presets.current;
  presetStatus.classList.toggle("pill--error", !!presets.error);
});

presetList.addEventListener("change", () => {
  if (presetList.value) {
    vst3go.presets.load(presetList.value);
  }
});
presetSave.addEventListener("click", () => {
  const name = presetName.value.trim();
  if (name) {
    vst3go.presets.save(name);
  }
});

function renderControl(param) {
  if (param.hidden) {
    return null;
  }

  const card = document.createElement("article");
  card.className = "card";
  if (param.readOnly) {
    card.classList.add("card--readonly");
  }
  card.dataset.controlState = param.readOnly ? "read-only" : "live";
  const label = document.createElement("p");
  label.className = "card__label";
  label.textContent = param.shortName;
  const title = document.createElement("h3");
  title.textContent = param.name;
  const unit = document.createElement("p");
  unit.textContent = param.unit || param.kind;
  card.append(label, title, unit);

  const field = document.createElement("div");
  field.className = "control";
  let show;

  if (param.kind === "choice") {
    const select = document.createElement("select");
    const steps = Math.max(param.stepCount, 1);
    for (let index = 0; index <= steps; index += 1) {
      const option = document.createElement("option");
      option.value = String(index / steps);
      option.textContent = param.stepCount > 0 ? String(index) : (index / steps).toFixed(2);
      select.appendChild(option);
    }
    select.disabled = param.readOnly;
    select.addEventListener("change", () => param.setNormalized(Number(select.value)));
    field.appendChild(select);
    show = () => {
      select.value = String(Math.round(param.normalized * steps) / steps);
    };
  } else {
    const range = document.createElement("input");
    range.type = "range";
    range.min = "0";
    range.max = "1";
    range.step = param.kind === "toggle" ? "1" : "0.001";
    range.disabled = param.readOnly;
    const value = document.createElement("input");
    value.type = "number";
    value.min = String(param.min);
    value.max = String(param.max);
    value.step = param.kind === "toggle" ? "1" : "0.001";
    value.disabled = param.readOnly;

    range.addEventListener("input", () => param.setNormalized(Number(range.value)));
    param.bindGesture(range);
    value.addEventListener("change", () => param.setPlain(Number(value.value)));
    field.append(range, value);
    show = () => {
      range.value = String(param.normalized);
      value.value = String(param.plain);
    };
  }

  const meta = document.createElement("div");
  meta.className = "control__meta";
  const kind = document.createElement("span");
  kind.textContent = param.kind;
  const readout = document.createElement("span");
  readout.className = "control__value";
  meta.append(kind, readout);
  if (param.readOnly) {
    const badge = document.createElement("span");
    badge.className = "control__badge";
    badge.textContent = "Read-only";
    meta.appendChild(badge);
  }
  card.append(field, meta);

  const update = () => {
    show();
    readout.textContent = displayText(param);
  };
  update();
  param.onChange(update);
  return card;
}

vst3go.sections.forEach((section) => {
  const wrapper = document.createElement("section");
  wrapper.className = "card";
  const heading = document.createElement("h2");
  heading.textContent = section.title;
  wrapper.appendChild(heading);
  const grid = document.createElement("div");
  grid.className = "grid";
  section.params.forEach((param) => {
    const card = renderControl(param);
    if (card) {
      grid.appendChild(card);
    }
  });
  wrapper.appendChild(grid);
  sectionsRoot.appendChild(wrapper);
});
