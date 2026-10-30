// TypeScript declarations of the vst3go editor SDK (window.vst3go), which
// vst3go injects into every editor page. Protocol: docs/web-editor-bridge.md.
// plugin.WriteEditorBindings writes this file next to the generated typed
// parameter module of a plugin.

/** The kind of control the plugin suggests for a parameter. */
type Vst3goParamKind = "slider" | "choice" | "toggle" | "meter" | "hidden";

/** A plugin parameter, kept in sync with the host and the plugin. */
interface Vst3goParam {
  readonly id: number;
  readonly name: string;
  readonly shortName: string;
  readonly unit: string;
  readonly kind: Vst3goParamKind;
  /** Plain range: continuous parameters span min..max. */
  readonly min: number;
  readonly max: number;
  /**
   * How a continuous range maps onto 0..1: evenly ("linear"), equal ratios
   * per equal travel ("log"), or min + (max - min) * n ** exponent ("power").
   */
  readonly scale: "linear" | "log" | "power";
  readonly exponent: number;
  /** Default as a plain value. */
  readonly defaultValue: number;
  readonly defaultNormalized: number;
  /** Number of steps; 0 for continuous parameters. */
  readonly stepCount: number;
  /** param.Flags bits. */
  readonly flags: number;
  /** Output (meter) parameters change only from the plugin. */
  readonly readOnly: boolean;
  readonly hidden: boolean;

  /** Current value, 0..1. */
  readonly normalized: number;
  /** Current value in the plain range. */
  readonly plain: number;
  /** Current value as the plugin's formatter writes it (it may include the unit). */
  readonly text: string;

  toPlain(normalized: number): number;
  toNormalized(plain: number): number;
  /** Starts an edit gesture (a drag): one automation pass, one undo step. Nests. */
  beginEdit(): void;
  /** Ends the gesture started by beginEdit. */
  endEdit(): void;
  /** Edits the value; outside a gesture it is a complete edit. */
  setNormalized(normalized: number): void;
  setPlain(plain: number): void;
  /** Sets the default value. */
  reset(): void;
  /** Calls listener after every change; returns the unsubscribe function. */
  onChange(listener: (param: Vst3goParam) => void): () => void;
  /** Makes pointer drags on element edit gestures; returns the unbind function. */
  bindGesture(element: Element): () => void;
}

interface Vst3goPluginInfo {
  readonly id: string;
  readonly name: string;
  readonly version: string;
  readonly vendor: string;
  readonly category: string;
}

interface Vst3goSection {
  readonly title: string;
  readonly params: readonly Vst3goParam[];
}

/** The user preset folder of the plugin (.vstpreset files hosts list too). */
interface Vst3goPresets {
  /** Preset names, "/" separating subfolders. */
  readonly list: readonly string[];
  /** Last preset saved or loaded. */
  readonly current: string;
  /** Why the last save or load failed, else "". */
  readonly error: string;
  refresh(): void;
  save(name: string): void;
  load(name: string): void;
  onChange(listener: (presets: Vst3goPresets) => void): () => void;
}

interface Vst3goEditorSize {
  /** Initial size in logical pixels. */
  readonly width: number;
  readonly height: number;
  /** Resize limits; 0 is unbounded. */
  readonly minWidth: number;
  readonly minHeight: number;
  readonly maxWidth: number;
  readonly maxHeight: number;
  readonly resizable: boolean;
}

interface Vst3go {
  readonly version: 1;
  readonly plugin: Vst3goPluginInfo;
  readonly sections: readonly Vst3goSection[];
  /** Every parameter, in registry order. */
  readonly params: readonly Vst3goParam[];
  /** Looks a parameter up by ID or by name. */
  param(key: number | string): Vst3goParam | undefined;
  /** Calls listener after any parameter change, from the plugin or the page. */
  onParamChange(listener: (param: Vst3goParam) => void): () => void;
  readonly presets: Vst3goPresets;
  /** Sends data (JSON) to the processor's EditorMessage(name, data). */
  send(name: string, data?: unknown): void;
  /** Receives the processor's Host.PostEditorMessage(name, data). */
  on<T = unknown>(name: string, listener: (data: T) => void): () => void;
  readonly size: Vst3goEditorSize;
  /** Asks the host for a new editor size, clamped to the limits. */
  resize(width: number, height: number): void;
  /**
   * Shows the platform's open file dialog; resolves to the chosen file's
   * path, or null when the user cancels.
   */
  chooseFile(options?: Vst3goChooseFileOptions): Promise<string | null>;
  /**
   * Shows the platform's folder dialog; resolves to the chosen folder's
   * path, or null when the user cancels. In a sandbox (AUv3) everything in
   * the folder becomes readable, in later runs too.
   */
  chooseFolder(options?: Vst3goChooseFolderOptions): Promise<string | null>;
}

interface Vst3goChooseFileOptions {
  /** The dialog's prompt. */
  title?: string;
  /** Accepted file name extensions without the dot ("wav"); any file without them. */
  extensions?: string[];
}

interface Vst3goChooseFolderOptions {
  /** The dialog's prompt. */
  title?: string;
}

interface Window {
  readonly vst3go: Vst3go;
}

declare const vst3go: Vst3go;
