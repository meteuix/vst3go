# Ableton Live test scope

The macOS build script builds AU and VST3. The locally installed VST3 is
Apple Silicon arm64. Mac VST3 pluginval strictness 5 passed. Ableton Live
itself has not been exercised. Windows and Intel Mac binaries are not supplied.

On Apple Silicon Live, enable VST3 system folders in Preferences/Settings,
rescan, and find Jovaris Track Meter Trial under Jovaris Audio. Start with
TestSignals/05_dynamic_reset_check.wav and compare dynamic L/R readings,
silence return, Peak Hold and Reset during playback and while stopped.
Test H/V resize and project save/reopen. Record Live version, OS and CPU with
results. No claim of completed Live validation is made.
