// vst3go editor SDK, injected into every editor page as window.vst3go.
// Types: vst3go.d.ts. Protocol: docs/web-editor-bridge.md.
(function () {
  "use strict";

  const boot = window.__vst3goBootstrap || { model: { plugin: {}, sections: [] }, editor: {} };

  // post sends a message to the plugin: the WKWebView/WebKitGTK script
  // message handler or WebView2.
  function post(message) {
    const webkit = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.vst3go;
    if (webkit) {
      webkit.postMessage(message);
    } else if (window.chrome && window.chrome.webview) {
      window.chrome.webview.postMessage(message);
    }
  }

  function clamp01(v) {
    v = Number(v);
    return v > 0 ? (v < 1 ? v : 1) : 0;
  }

  // notify calls every listener, isolating their errors.
  function notify(listeners, ...args) {
    listeners.forEach((listener) => {
      try {
        listener(...args);
      } catch (error) {
        console.error(error);
      }
    });
  }

  function subscribe(listeners, listener) {
    listeners.add(listener);
    return () => listeners.delete(listener);
  }

  class Param {
    constructor(control) {
      this.id = control.id;
      this.name = control.name;
      this.shortName = control.shortName || control.name;
      this.unit = control.unit || "";
      this.kind = control.kind;
      this.min = control.min;
      this.max = control.max;
      this.defaultNormalized = control.defaultValue; // EditorControl.defaultValue is normalized
      this.scale = control.scale || "linear";
      this.exponent = control.exponent || 1;
      this.stepCount = control.stepCount || 0;
      this.flags = control.flags;
      this.readOnly = !!control.readOnly;
      this.hidden = !!control.hidden;
      this._normalized = control.normalized;
      this._plain = control.plain;
      this._text = control.text || "";
      this._gestures = 0;
      this._listeners = new Set();
    }

    get normalized() { return this._normalized; }
    get plain() { return this._plain; }
    // text is the value as the plugin's formatter writes it.
    get text() { return this._text; }
    get defaultValue() { return this.toPlain(this.defaultNormalized); }

    // toPlain converts a normalized value like the plugin: stepped
    // parameters snap to min(steps, floor(n * (steps + 1))).
    toPlain(normalized) {
      const n = clamp01(normalized);
      if (this.stepCount > 0) {
        const step = Math.min(this.stepCount, Math.floor(n * (this.stepCount + 1)));
        return this.min + (step * (this.max - this.min)) / this.stepCount;
      }
      if (this.scale === "log" && this.min > 0) return this.min * Math.pow(this.max / this.min, n);
      if (this.scale === "power") return this.min + (this.max - this.min) * Math.pow(n, this.exponent);
      return this.min + n * (this.max - this.min);
    }

    toNormalized(plain) {
      const v = Number(plain);
      if (!(this.max > this.min) || !(v > this.min)) return 0;
      if (v >= this.max) return 1;
      if (this.stepCount > 0) return (v - this.min) / (this.max - this.min);
      if (this.scale === "log") return Math.log(v / this.min) / Math.log(this.max / this.min);
      if (this.scale === "power") return Math.pow((v - this.min) / (this.max - this.min), 1 / this.exponent);
      return (v - this.min) / (this.max - this.min);
    }

    // beginEdit and endEdit bracket a drag, so the host records it as one
    // automation pass and one undo step. They nest.
    beginEdit() {
      if (this.readOnly) return;
      if (this._gestures++ === 0) post({ type: "begin-edit", id: this.id });
    }

    endEdit() {
      if (this._gestures === 0) return;
      if (--this._gestures === 0) post({ type: "end-edit", id: this.id });
    }

    // setNormalized edits the parameter; outside beginEdit/endEdit it is a
    // complete edit on its own. The plugin answers with the formatted text.
    setNormalized(normalized) {
      if (this.readOnly) return;
      const n = clamp01(normalized);
      this._set(n, this.toPlain(n), this._text);
      post({ type: "param-change", id: this.id, value: n });
    }

    setPlain(plain) {
      this.setNormalized(this.toNormalized(plain));
    }

    reset() {
      this.setNormalized(this.defaultNormalized);
    }

    // onChange calls listener(param) after every change, from the plugin or
    // the page; it returns the unsubscribe function.
    onChange(listener) {
      return subscribe(this._listeners, listener);
    }

    // bindGesture makes pointer drags on element edit gestures of this
    // parameter; it returns the function that unbinds it.
    bindGesture(element) {
      let active = false;
      const down = (event) => {
        if (active) return;
        active = true;
        if (element.setPointerCapture && event.pointerId !== undefined) {
          try { element.setPointerCapture(event.pointerId); } catch (_) { /* synthetic events */ }
        }
        this.beginEdit();
      };
      const up = () => {
        if (!active) return;
        active = false;
        this.endEdit();
      };
      const events = [["pointerdown", down], ["pointerup", up], ["pointercancel", up], ["lostpointercapture", up]];
      events.forEach(([type, handler]) => element.addEventListener(type, handler));
      return () => {
        up();
        events.forEach(([type, handler]) => element.removeEventListener(type, handler));
      };
    }

    _set(normalized, plain, text) {
      this._normalized = normalized;
      this._plain = plain;
      this._text = text;
      notify(this._listeners, this);
      notify(changeListeners, this);
    }
  }

  const changeListeners = new Set();
  const params = [];
  const byID = new Map();
  const byName = new Map();
  (boot.model.sections || []).forEach((section) => {
    (section.controls || []).forEach((control) => {
      const p = new Param(control);
      params.push(p);
      byID.set(p.id, p);
      if (!byName.has(p.name)) byName.set(p.name, p);
    });
  });

  const presetListeners = new Set();
  const presets = {
    list: [],
    current: "",
    error: "",
    refresh() { post({ type: "preset-list" }); },
    save(name) { post({ type: "preset-save", name: String(name) }); },
    load(name) { post({ type: "preset-load", name: String(name) }); },
    onChange(listener) { return subscribe(presetListeners, listener); },
  };

  const messageListeners = new Map();
  const fileRequests = new Map();
  let nextFileRequest = 1;
  // chooseFile asks the plugin for a dialog; the "file-chosen" answer
  // settles the promise.
  const chooseFile = (title, data) => {
    const id = nextFileRequest++;
    return new Promise((resolve, reject) => {
      fileRequests.set(id, { resolve, reject });
      post({ type: "choose-file", id, name: title, data });
    });
  };
  const size = Object.assign({ width: 0, height: 0, minWidth: 0, minHeight: 0, maxWidth: 0, maxHeight: 0, resizable: true }, boot.editor);

  const sdk = {
    version: 1,
    plugin: boot.model.plugin || {},
    sections: (boot.model.sections || []).map((section) => ({
      title: section.title,
      params: (section.controls || []).map((control) => byID.get(control.id)),
    })),
    params,
    // param looks a parameter up by ID or by name.
    param(key) {
      return typeof key === "number" ? byID.get(key) : byName.get(key);
    },
    // onParamChange calls listener(param) after any parameter change, from
    // the plugin or the page.
    onParamChange(listener) { return subscribe(changeListeners, listener); },
    presets,
    // send delivers data to the processor's EditorMessage(name, data).
    send(name, data) {
      post({ type: "message", name: String(name), data: data === undefined ? null : data });
    },
    // on receives the processor's Host.PostEditorMessage(name, data).
    on(name, listener) {
      if (!messageListeners.has(name)) messageListeners.set(name, new Set());
      return subscribe(messageListeners.get(name), listener);
    },
    // size is the editor's initial size and resize limits.
    size,
    // resize asks the host for a new editor size, within the limits.
    resize(width, height) {
      if (size.resizable) post({ type: "resize", width: Math.round(width), height: Math.round(height) });
    },
    // chooseFile shows the platform's open file dialog and resolves to the
    // chosen file's path, or null when the user cancels. options.title is
    // the prompt, options.extensions the accepted file name extensions
    // (["wav", "flac"]; any file without them).
    chooseFile(options) {
      const opts = options || {};
      return chooseFile(String(opts.title || ""), { extensions: (opts.extensions || []).map(String) });
    },
    // chooseFolder shows the platform's folder dialog and resolves to the
    // chosen folder's path, or null when the user cancels. In a sandbox
    // (AUv3) everything in the folder becomes readable, in later runs too:
    // the samples an instrument file names, for example. options.title is
    // the prompt.
    chooseFolder(options) {
      const opts = options || {};
      return chooseFile(String(opts.title || ""), { folder: true });
    },
  };

  // __vst3goReceive handles the messages of the plugin.
  window.__vst3goReceive = function (message) {
    if (!message) return;
    switch (message.type) {
      case "param-change": {
        const p = byID.get(message.id);
        if (p) p._set(Number(message.normalized), Number(message.plain), message.text || "");
        break;
      }
      case "presets":
        presets.list = message.presets || [];
        presets.current = message.current || "";
        presets.error = message.error || "";
        notify(presetListeners, presets);
        break;
      case "message": {
        const listeners = messageListeners.get(message.name);
        if (listeners) notify(listeners, message.data);
        break;
      }
      case "file-chosen": {
        const request = fileRequests.get(message.id);
        if (!request) break;
        fileRequests.delete(message.id);
        if (message.error) request.reject(new Error(message.error));
        else request.resolve(message.path || null);
        break;
      }
    }
  };

  if (window.chrome && window.chrome.webview) {
    window.chrome.webview.addEventListener("message", (event) => {
      window.__vst3goReceive(typeof event.data === "string" ? JSON.parse(event.data) : event.data);
    });
  }

  window.vst3go = Object.freeze(sdk);
  presets.refresh();
})();
