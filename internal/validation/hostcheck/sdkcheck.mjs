// sdkcheck runs the editor SDK (pkg/plugin/editor_assets/vst3go.js) in a
// fake page and checks its protocol: messages posted for edits, gestures,
// presets, custom messages, resizing and file dialogs, and the handling of
// the plugin's messages. scripts/check_editor_sdk.sh runs it with node.
import { readFileSync } from "node:fs";
import vm from "node:vm";
import assert from "node:assert/strict";

const source = readFileSync(new URL("../../../pkg/plugin/editor_assets/vst3go.js", import.meta.url), "utf8");

function page(bootstrap, transport = "webkit") {
  const posted = [];
  const listeners = [];
  const window = { __vst3goBootstrap: bootstrap };
  if (transport === "webkit") {
    window.webkit = { messageHandlers: { vst3go: { postMessage: (m) => posted.push(structuredClone(m)) } } };
  } else {
    window.chrome = { webview: { postMessage: (m) => posted.push(structuredClone(m)), addEventListener: (type, l) => listeners.push([type, l]) } };
  }
  const context = vm.createContext({ window, console });
  vm.runInContext(source, context);
  return { sdk: window.vst3go, posted, window, listeners };
}

const control = (id, name, extra = {}) => ({
  id, name, shortName: name.slice(0, 3), unit: "", kind: "slider", normalized: 0, plain: 0, text: "0",
  min: 0, max: 1, defaultValue: 0, stepCount: 0, flags: 0, readOnly: false, hidden: false, ...extra,
});

const bootstrap = {
  model: {
    plugin: { id: "com.example", name: "Example", version: "1", vendor: "V", category: "Fx" },
    sections: [{ title: "Main", controls: [
      control(1, "Gain", { unit: "dB", min: -24, max: 24, defaultValue: 0.5, normalized: 0.5, text: "0.0 dB" }),
      control(2, "Mode", { kind: "choice", min: 0, max: 3, stepCount: 3 }),
      control(3, "Level", { kind: "meter", readOnly: true }),
      control(4, "Cutoff", { unit: "Hz", min: 20, max: 20000, scale: "log" }),
      control(5, "Time", { unit: "ms", min: 0, max: 1000, scale: "power", exponent: 3 }),
    ] }],
  },
  editor: { width: 600, height: 400, minWidth: 300, minHeight: 200, maxWidth: 0, maxHeight: 0, resizable: true },
};

{
  const { sdk, posted, window } = page(bootstrap);
  assert.deepEqual(posted, [{ type: "preset-list" }], "the SDK asks for the presets on load");
  posted.length = 0;
  assert.equal(sdk.version, 1);
  assert.equal(sdk.plugin.name, "Example");
  assert.equal(sdk.params.length, 5);
  assert.equal(sdk.sections[0].params[1], sdk.param(2));
  assert.equal(sdk.param("Gain"), sdk.param(1));
  assert.equal(sdk.param("nope"), undefined);
  assert.ok(Object.isFrozen(sdk));

  const gain = sdk.param(1);
  assert.equal(gain.plain, 0);
  assert.equal(gain.text, "0.0 dB");
  assert.equal(gain.defaultNormalized, 0.5);
  assert.equal(gain.defaultValue, 0);
  assert.equal(gain.toPlain(0.75), 12);
  assert.equal(gain.toNormalized(-48), 0, "toNormalized clamps");

  // Stepped parameters snap like the plugin: min(steps, floor(n * (steps + 1))).
  const mode = sdk.param(2);
  assert.deepEqual([0, 0.24, 0.25, 0.5, 0.74, 0.75, 1].map((n) => mode.toPlain(n)), [0, 0, 1, 2, 2, 3, 3]);

  // Logarithmic and power scales map like the plugin.
  const near = (a, b) => Math.abs(a - b) < 1e-9 * Math.max(1, Math.abs(b));
  const cutoff = sdk.param(4);
  assert.ok(near(cutoff.toPlain(0.5), Math.sqrt(20 * 20000)), "log midpoint is the geometric mean");
  assert.ok(near(cutoff.toNormalized(632.4555320336759), 0.5));
  assert.equal(cutoff.toNormalized(10), 0);
  assert.equal(cutoff.toNormalized(30000), 1);
  const time = sdk.param(5);
  assert.ok(near(time.toPlain(0.5), 125));
  assert.ok(near(time.toNormalized(125), 0.5));

  // Edits and nested gestures.
  const seen = [];
  const unsubscribe = gain.onChange((p) => seen.push(p.normalized));
  const all = [];
  sdk.onParamChange((p) => all.push(p.id));
  gain.beginEdit();
  gain.beginEdit();
  gain.setNormalized(1.5);
  gain.endEdit();
  gain.endEdit();
  gain.endEdit();
  assert.deepEqual(posted, [
    { type: "begin-edit", id: 1 },
    { type: "param-change", id: 1, value: 1 },
    { type: "end-edit", id: 1 },
  ]);
  assert.equal(gain.plain, 24, "page edits update the value right away");
  posted.length = 0;
  gain.setPlain(-12);
  assert.deepEqual(posted, [{ type: "param-change", id: 1, value: 0.25 }]);
  posted.length = 0;
  gain.reset();
  assert.deepEqual(posted, [{ type: "param-change", id: 1, value: 0.5 }]);
  unsubscribe();
  assert.deepEqual(seen, [1, 0.25, 0.5]);

  // Read-only parameters are not edited.
  posted.length = 0;
  const level = sdk.param(3);
  level.beginEdit();
  level.setNormalized(1);
  level.endEdit();
  assert.deepEqual(posted, []);

  // The plugin's messages.
  window.__vst3goReceive({ type: "param-change", id: 3, normalized: 0.8, plain: 0.8, text: "-2 dB" });
  assert.equal(level.normalized, 0.8);
  assert.equal(level.text, "-2 dB");
  assert.deepEqual(all, [1, 1, 1, 3]);
  window.__vst3goReceive({ type: "param-change", id: 99, normalized: 1, plain: 1 });
  window.__vst3goReceive(null);

  const presetEvents = [];
  sdk.presets.onChange((presets) => presetEvents.push([...presets.list, presets.current, presets.error]));
  window.__vst3goReceive({ type: "presets", presets: ["A", "B/C"], current: "A" });
  window.__vst3goReceive({ type: "presets", error: "disk full" });
  assert.deepEqual(presetEvents, [["A", "B/C", "A", ""], ["", "disk full"]]);
  posted.length = 0;
  sdk.presets.save("B/C");
  sdk.presets.load("A");
  sdk.presets.refresh();
  assert.deepEqual(posted, [{ type: "preset-save", name: "B/C" }, { type: "preset-load", name: "A" }, { type: "preset-list" }]);

  // Custom messages, with listener errors isolated.
  const got = [];
  const errors = console.error;
  console.error = () => {};
  sdk.on("status", () => { throw new Error("listener bug"); });
  const off = sdk.on("status", (data) => got.push(data));
  window.__vst3goReceive({ type: "message", name: "status", data: { voices: 2 } });
  off();
  window.__vst3goReceive({ type: "message", name: "status", data: { voices: 3 } });
  console.error = errors;
  assert.deepEqual(got, [{ voices: 2 }]);
  posted.length = 0;
  sdk.send("note", { key: 60 });
  sdk.send("ping");
  assert.deepEqual(posted, [{ type: "message", name: "note", data: { key: 60 } }, { type: "message", name: "ping", data: null }]);

  // Resizing.
  posted.length = 0;
  assert.equal(sdk.size.width, 600);
  sdk.resize(700.4, 500.6);
  assert.deepEqual(posted, [{ type: "resize", width: 700, height: 501 }]);

  // chooseFile: one request per call, answered by its id.
  posted.length = 0;
  const first = sdk.chooseFile({ title: "Model", extensions: ["nam", 7] });
  const second = sdk.chooseFile();
  assert.deepEqual(posted, [
    { type: "choose-file", id: 1, name: "Model", data: { extensions: ["nam", "7"] } },
    { type: "choose-file", id: 2, name: "", data: { extensions: [] } },
  ]);
  window.__vst3goReceive({ type: "file-chosen", id: 2, path: "" });
  window.__vst3goReceive({ type: "file-chosen", id: 1, path: "/models/amp.nam" });
  window.__vst3goReceive({ type: "file-chosen", id: 1, path: "/again" }); // answered already
  assert.equal(await first, "/models/amp.nam");
  assert.equal(await second, null, "cancelled");
  const failed = sdk.chooseFile({ extensions: ["a/b"] });
  window.__vst3goReceive({ type: "file-chosen", id: 3, path: "", error: "choose-file: invalid extension" });
  await assert.rejects(failed, /invalid extension/);
}

{
  // Fixed-size editors do not ask for resizing; WebView2 delivers messages
  // as events.
  const fixed = structuredClone(bootstrap);
  fixed.editor.resizable = false;
  const { sdk, posted, listeners } = page(fixed, "webview2");
  posted.length = 0;
  sdk.resize(100, 100);
  assert.deepEqual(posted, []);
  assert.equal(listeners.length, 1);
  const [type, listener] = listeners[0];
  assert.equal(type, "message");
  listener({ data: JSON.stringify({ type: "param-change", id: 1, normalized: 0, plain: -24, text: "-24 dB" }) });
  listener({ data: { type: "param-change", id: 2, normalized: 1, plain: 3, text: "3" } });
  assert.equal(sdk.param(1).text, "-24 dB");
  assert.equal(sdk.param(2).plain, 3);
}

{
  // Without bootstrap data the SDK still loads.
  const { sdk } = page(undefined);
  assert.equal(sdk.params.length, 0);
}

console.log("sdk ok");
