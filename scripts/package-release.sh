#!/usr/bin/env bash
set -euo pipefail

release_version="${1:-}"
if [[ -z "${release_version}" || ! "${release_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 <version, e.g. 1.0.0-rc.1>" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_dir="${repo_root}/release"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/yundu-release.XXXXXX")"
trap 'rm -rf -- "${work_dir}"' EXIT

cd "${repo_root}"
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web run build
cp -a web/dist/. internal/web/dist/
go test ./...
go vet ./...

mkdir -p "${release_dir}"
architectures=(amd64 arm64)
for architecture in "${architectures[@]}"; do
  package_name="yundu-server-v${release_version}-linux-${architecture}"
  package_root="${work_dir}/${package_name}"
  mkdir -p "${package_root}/bin" "${package_root}/config" "${package_root}/docs" "${package_root}/runtime"

  CGO_ENABLED=0 GOOS=linux GOARCH="${architecture}" go build \
    -buildvcs=false -trimpath \
    -ldflags "-s -w -X main.version=v${release_version}" \
    -o "${package_root}/bin/yundu-server" ./cmd/yundu-web

  cp deploy/release/config.yaml "${package_root}/config/config.yaml"
  cp deploy/release/env.example "${package_root}/config/env.example"
  cp README.md "${package_root}/README.md"
  cp docs/product-guide.md docs/user-guide.md docs/operations-runbook.md docs/database-recovery.md "${package_root}/docs/"
  chmod 0755 "${package_root}/bin/yundu-server"
  tar -C "${work_dir}" -czf "${release_dir}/${package_name}.tar.gz" "${package_name}"
done

(
  cd "${release_dir}"
  sha256sum "yundu-server-v${release_version}-linux-amd64.tar.gz" "yundu-server-v${release_version}-linux-arm64.tar.gz" > "SHA256SUMS-v${release_version}.txt"
)

echo "release packages written to ${release_dir}"
