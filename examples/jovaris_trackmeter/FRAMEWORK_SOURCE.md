# Framework provenance

Repository: https://github.com/meteuix/vst3go
Baseline: 852f8bab33283fda7953b8f9b43468a3b091d58f

The original handoff vendored that snapshot. This in-repository example uses
its containing checkout through `replace ../..`.

Framework changes: Darwin WebKit lifecycle/error diagnostics; CLAP/AU meter
refresh scheduling with regression tests; optional StateParameterProcessor
registry for selective persistence. Original patches and product 0.1.4 patch
are retained in `patches/`. Module v2.0.0 is a replace placeholder, not a tag
identifying this baseline. Existing framework licenses are unchanged.
