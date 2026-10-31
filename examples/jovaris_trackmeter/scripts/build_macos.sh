#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ "$(uname -s)" != Darwin ]]; then
  echo '이 스크립트는 Mac AU/VST3 시험 빌드용입니다.' >&2
  exit 1
fi
# The official Go pkg updates new shells. Reuse it in an already-open
# Terminal too, instead of asking the tester to repair PATH by hand.
if ! command -v go >/dev/null; then
  for go_bin_dir in /usr/local/go/bin /opt/homebrew/bin /usr/local/bin; do
    if [[ -x "$go_bin_dir/go" ]]; then
      export PATH="$go_bin_dir:$PATH"
      break
    fi
  done
fi
command -v go >/dev/null || { echo 'Go 1.24.3 이상이 필요합니다.' >&2; exit 1; }
xcrun --find clang >/dev/null
if [[ "$(go env GOARCH)" != arm64 ]]; then
  echo '이 시험판은 Apple Silicon 네이티브 Go 환경에서 실행하세요.' >&2
  exit 1
fi
export CGO_ENABLED=1
# ZIP source has no Git metadata. Disable stamping instead of inventing it.
export GOFLAGS="${GOFLAGS:-} -buildvcs=false"
go test ./...
install_flag=()
if [[ "${1:-}" == --install ]]; then
  install_flag=(-install)
elif [[ "$#" != 0 ]]; then
  echo '사용법: bash scripts/build_macos.sh [--install]' >&2
  exit 1
fi
go run github.com/meteuix/vst3go/v2/cmd/vst3go build \
  -format au,vst3 -version 0.1.4 -id audio.jovaris.trackmeter.vst3go.trial \
  ${install_flag[@]+"${install_flag[@]}"} . JovarisTrackMeterTrial
