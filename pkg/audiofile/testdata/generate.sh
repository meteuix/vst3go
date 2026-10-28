#!/usr/bin/env bash
# Regenerates the decoder fixtures from a reference signal, with encoders
# other than this package: afconvert (macOS) for WAV and AIFF variants, and
# flac, oggenc, lame in a Debian container. The signal is 2048
# stereo frames at 44.1 kHz: left 0.5*sin(2*pi*440*n/44100), right -left.
set -euo pipefail
cd "$(dirname "$0")"

python3 - <<'PY'
import math, wave
N = 2048
for bits in (16, 24):
    out = bytearray()
    for n in range(N):
        v = 0.5 * math.sin(2 * math.pi * 440 * n / 44100)
        for x in (v, -v):
            out += int(round(x * 2 ** (bits - 1))).to_bytes(bits // 8, "little", signed=True)
    w = wave.open(f"ref{bits}.wav", "wb")
    w.setnchannels(2)
    w.setsampwidth(bits // 8)
    w.setframerate(44100)
    w.writeframes(bytes(out))
    w.close()
PY

afconvert -f AIFF -d BEI24 ref24.wav aiff24.aif
afconvert -f AIFC -d BEF32 ref24.wav float32.aifc
afconvert -f WAVE -d LEF32 ref24.wav float32.wav
afconvert -f WAVE -d LEF64 ref24.wav float64.wav
afconvert -f WAVE -d UI8 ref16.wav u8.wav
afconvert -f RF64 -d LEI24 ref24.wav rf64.wav

docker run --rm --platform linux/arm64 -v "$PWD":/w -w /w debian:bookworm sh -ec '
apt-get update >/dev/null
apt-get install -y --no-install-recommends flac vorbis-tools lame >/dev/null 2>&1
flac -s -f -o flac16.flac ref16.wav
flac -s -f -o flac24.flac ref24.wav
oggenc -Q -q 4 -o vorbis.ogg ref16.wav
lame --quiet -b 128 --tt Sine --id3v2-only ref16.wav id3.mp3
chown '"$(id -u):$(id -g)"' flac16.flac flac24.flac vorbis.ogg id3.mp3'
