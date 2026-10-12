#!/usr/bin/env bash
set -euo pipefail

readonly expected_buf_version="1.66.1"
readonly expected_go_version="go1.26.4"
readonly expected_protoc_gen_go_version="v1.36.12"
readonly expected_protoc_gen_go_grpc_version="1.6.2"
readonly expected_grpcio_tools_version="1.84.0"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
contracts_dir="$repo_root/contracts"
daemon_dir="$repo_root/session-daemon-go"
app_dir="$repo_root/app-server-go"

native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -m "$1"
  else
    realpath "$1"
  fi
}

resolve_tool() {
  local requested="$1"
  local resolved
  if [[ "$requested" == */* || "$requested" == *\\* ]]; then
    if [[ ! -f "$requested" ]]; then
      printf 'ERROR: required tool does not exist: %s\n' "$requested" >&2
      return 1
    fi
    resolved="$requested"
  else
    resolved="$(command -v "$requested" 2>/dev/null || true)"
    if [[ -z "$resolved" ]]; then
      printf 'ERROR: required tool is not on PATH: %s\n' "$requested" >&2
      return 1
    fi
  fi
  native_path "$resolved"
}

buf_bin="$(resolve_tool "${ASTRORDER_BUF:-buf}")"
go_bin="$(resolve_tool "${ASTRORDER_GO:-go}")"
protoc_gen_go_bin="$(resolve_tool "${ASTRORDER_PROTOC_GEN_GO:-protoc-gen-go}")"
protoc_gen_go_grpc_bin="$(resolve_tool "${ASTRORDER_PROTOC_GEN_GO_GRPC:-protoc-gen-go-grpc}")"

go_only="${ASTRORDER_GO_ONLY:-1}"
case "$go_only" in
  0|1) ;;
  *) printf 'ERROR: ASTRORDER_GO_ONLY must be 0 or 1.\n' >&2; exit 2 ;;
esac
python_bin=""
if [[ "$go_only" == "0" ]]; then
  python_bin="$(resolve_tool "${ASTRORDER_GRPC_TOOLS_PYTHON:-python}")"
fi

output_root="${ASTRORDER_GO_OUTPUT_DIR:-$repo_root/.runtime/go-build}"
mkdir -p -- "$output_root"
output_root="$(cd -- "$output_root" && pwd)"
partial_dir="$(mktemp -d "$output_root/.astrorder-go-build.partial.XXXXXX")"
published_dir=""
cleanup() {
  if [[ -n "$partial_dir" && -d "$partial_dir" ]]; then
    rm -rf -- "$partial_dir"
  fi
}
trap cleanup EXIT

clean_env=(env -i "PATH=${PATH:-}" "GOENV=off" "GOTOOLCHAIN=local")
for name in PATHEXT SYSTEMROOT WINDIR TEMP TMP TMPDIR HOME USERPROFILE LOCALAPPDATA GOCACHE GOMODCACHE GOPATH; do
  if [[ -n "${!name:-}" ]]; then
    clean_env+=("$name=${!name}")
  fi
done

run_clean() {
  local step="$1"
  local directory="$2"
  shift 2
  printf '==> %s\n' "$step"
  set +e
  (cd -- "$directory" && "${clean_env[@]}" "$@")
  local status=$?
  set -e
  if [[ $status -ne 0 ]]; then
    printf 'ERROR: %s failed with exit code %d.\n' "$step" "$status" >&2
    exit "$status"
  fi
}

capture_version() {
  local step="$1"
  shift
  local output
  set +e
  output="$("${clean_env[@]}" "$@" 2>&1)"
  local status=$?
  set -e
  if [[ $status -ne 0 ]]; then
    printf 'ERROR: %s failed with exit code %d.\n%s\n' "$step" "$status" "$output" >&2
    exit "$status"
  fi
  printf '%s' "$output"
}

buf_version="$(capture_version 'Buf version check' "$buf_bin" --version)"
[[ "$buf_version" == "$expected_buf_version" ]] || { printf 'ERROR: Buf %s is required; got %s.\n' "$expected_buf_version" "$buf_version" >&2; exit 1; }
go_version="$(capture_version 'Go version check' "$go_bin" version)"
[[ "$go_version" == *" $expected_go_version "* ]] || { printf 'ERROR: Go %s is required; got %s.\n' "$expected_go_version" "$go_version" >&2; exit 1; }
go_plugin_version="$(capture_version 'protoc-gen-go version check' "$protoc_gen_go_bin" --version)"
[[ "$go_plugin_version" =~ ^protoc-gen-go(\.exe)?[[:space:]]+$expected_protoc_gen_go_version$ ]] || { printf 'ERROR: protoc-gen-go %s is required; got %s.\n' "$expected_protoc_gen_go_version" "$go_plugin_version" >&2; exit 1; }
grpc_plugin_version="$(capture_version 'protoc-gen-go-grpc version check' "$protoc_gen_go_grpc_bin" --version)"
[[ "$grpc_plugin_version" =~ ^protoc-gen-go-grpc[[:space:]]+$expected_protoc_gen_go_grpc_version$ ]] || { printf 'ERROR: protoc-gen-go-grpc %s is required; got %s.\n' "$expected_protoc_gen_go_grpc_version" "$grpc_plugin_version" >&2; exit 1; }

contract_env=(
  "ASTRORDER_VERIFY_GENERATION=1"
  "ASTRORDER_BUF=$buf_bin"
  "ASTRORDER_PROTOC_GEN_GO=$protoc_gen_go_bin"
  "ASTRORDER_PROTOC_GEN_GO_GRPC=$protoc_gen_go_grpc_bin"
)
if [[ "$go_only" == "0" ]]; then
  run_clean "Python grpcio/grpcio-tools version check" "$contracts_dir" "$python_bin" -c "import grpc, importlib.metadata; assert grpc.__version__ == '$expected_grpcio_tools_version', grpc.__version__; assert importlib.metadata.version('grpcio-tools') == '$expected_grpcio_tools_version', importlib.metadata.version('grpcio-tools')"
  contract_env+=("ASTRORDER_GENERATE_PYTHON=1" "ASTRORDER_GRPC_TOOLS_PYTHON=$python_bin" "ASTRORDER_INTEROP_PYTHON=$python_bin")
else
  contract_env+=("ASTRORDER_GENERATE_PYTHON=0")
fi

run_clean "Buf lint" "$contracts_dir" "$buf_bin" lint
run_clean "Buf descriptor build" "$contracts_dir" "$buf_bin" build
run_clean "Contract tests and deterministic generation" "$contracts_dir" "${contract_env[@]}" "$go_bin" test ./... -count=1
run_clean "Contract vet" "$contracts_dir" "$go_bin" vet ./...
run_clean "Session Daemon tests" "$daemon_dir" "$go_bin" test ./... -count=1
run_clean "Session Daemon vet" "$daemon_dir" "$go_bin" vet ./...
run_clean "App Server tests" "$app_dir" "$go_bin" test ./... -count=1
run_clean "App Server vet" "$app_dir" "$go_bin" vet ./...

partial_native="$(native_path "$partial_dir")"
run_clean "Build astrorder-sessiond.exe" "$daemon_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-sessiond.exe" ./cmd/astrorder-sessiond
run_clean "Build astrorder-app.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-app.exe" ./cmd/astrorder-app
run_clean "Build astrorderctl.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorderctl.exe" ./cmd/astrorderctl
run_clean "Build astrorder-agent.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-agent.exe" ./cmd/astrorder-agent
run_clean "Build astrorder-mcp.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-mcp.exe" ./cmd/astrorder-mcp
run_clean "Build astrorder-helper.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-helper.exe" ./cmd/astrorder-helper
run_clean "Build astrorder-model-helper.exe" "$daemon_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-model-helper.exe" ./cmd/astrorder-model-helper
run_clean "Build astrorder-memory-helper.exe" "$app_dir" "$go_bin" build -trimpath -o "$partial_native/astrorder-memory-helper.exe" ./cmd/astrorder-memory-helper

for helper_arch in amd64 arm64; do
  helper_platform="linux-$helper_arch"
  mkdir -p -- "$partial_dir/helpers/$helper_platform"
  run_clean "Build model helper for $helper_platform" "$daemon_dir" \
    "GOOS=linux" "GOARCH=$helper_arch" "CGO_ENABLED=0" "$go_bin" build -trimpath \
    -o "$partial_native/helpers/$helper_platform/astrorder-model-helper" ./cmd/astrorder-model-helper
  (cd -- "$partial_dir/helpers/$helper_platform" && sha256sum --text astrorder-model-helper > SHA256SUMS)
  run_clean "Build target helper for $helper_platform" "$app_dir" \
    "GOOS=linux" "GOARCH=$helper_arch" "CGO_ENABLED=0" "$go_bin" build -trimpath \
    -o "$partial_native/helpers/$helper_platform/astrorder-helper" ./cmd/astrorder-helper
done

(
  cd -- "$partial_dir"
  sha256sum \
    astrorder-sessiond.exe \
    astrorder-app.exe \
    astrorderctl.exe \
    astrorder-agent.exe \
    astrorder-mcp.exe \
    astrorder-helper.exe \
    astrorder-model-helper.exe \
    astrorder-memory-helper.exe \
    helpers/linux-amd64/astrorder-model-helper \
    helpers/linux-arm64/astrorder-model-helper \
    helpers/linux-amd64/astrorder-helper \
    helpers/linux-arm64/astrorder-helper > SHA256SUMS
)

candidate_name="candidate-$(date -u +%Y%m%dT%H%M%SZ)-${partial_dir##*.}"
published_dir="$output_root/$candidate_name"
if [[ -e "$published_dir" ]]; then
  printf 'ERROR: refusing to replace existing output: %s\n' "$published_dir" >&2
  exit 1
fi
mv -- "$partial_dir" "$published_dir"
partial_dir=""
printf 'Candidate artifacts retained at %s\n' "$(native_path "$published_dir")"
printf 'SHA-256 manifest: %s\n' "$(native_path "$published_dir/SHA256SUMS")"
