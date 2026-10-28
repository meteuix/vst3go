// Type-checks the editor SDK declarations and the generated bindings
// against typical editor code (scripts/check_editor_sdk.sh, tsc --noEmit).
import { param, params, type ParamKey } from "./params";

const volume: Vst3goParam = param("volume");
const keys: ParamKey[] = ["volume", "release", "shape", "program"];
const id: 0 = params.volume.id;
const kind: "choice" = params.shape.kind;

volume.beginEdit();
volume.setNormalized(volume.normalized + 0.1);
volume.setPlain(volume.toPlain(0.5));
volume.endEdit();
volume.reset();
const unsubscribe: () => void = volume.onChange((p) => console.log(p.text, p.plain, p.unit));
unsubscribe();
const unbind = param("release").bindGesture(document.createElement("div"));
unbind();

const found: Vst3goParam | undefined = vst3go.param("Program") ?? window.vst3go.param(params.program.id);
vst3go.onParamChange((p) => console.log(p.id, p.normalized));
vst3go.sections.forEach((section) => section.params.forEach((p) => console.log(section.title, p.name)));

interface Status { voices: number; learnedCC: number }
vst3go.on<Status>("status", (s) => console.log(s.voices + s.learnedCC));
vst3go.send("status");
vst3go.send("note", { key: 60 });

vst3go.presets.onChange((presets) => console.log(presets.list.join(), presets.current, presets.error));
vst3go.presets.save("Bass/Deep");
vst3go.presets.load(vst3go.presets.list[0]);
vst3go.presets.refresh();

if (vst3go.size.resizable) vst3go.resize(vst3go.size.minWidth, vst3go.size.minHeight);
console.log(vst3go.version, vst3go.plugin.name, keys, id, kind, found);

// @ts-expect-error unknown parameter keys do not compile
param("cutoff");
// @ts-expect-error parameters are read-only
volume.normalized = 1;
// @ts-expect-error resize needs a size
vst3go.resize();
