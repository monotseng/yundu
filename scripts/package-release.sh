#!/usr/bin/env bash
set -euo pipefail

release_version="${1:-}"
if [[ -z "${release_version}" || ! "${release_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: $0 <version, e.g. 1.0.0-rc.5>" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_dir="${repo_root}/release"
release_notes="${repo_root}/docs/release-notes-v${release_version}.md"
if [[ ! -f "${release_notes}" ]]; then
  echo "release notes not found: ${release_notes}" >&2
  exit 2
fi
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/yundu-release.XXXXXX")"
trap 'rm -rf -- "${work_dir}"' EXIT

cd "${repo_root}"
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web run build
find internal/web/dist -mindepth 1 -delete
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
  cp README.en.md "${package_root}/README.en.md"
  cp docs/deployment-guide.md docs/product-guide.md docs/user-guide.md docs/operations-runbook.md docs/database-recovery.md docs/frontend-dependency-audit.md "${release_notes}" "${package_root}/docs/"
  printf 'version=v%s\nos=linux\narch=%s\nbinary=bin/yundu-server\nconfig=config/config.yaml\n' "${release_version}" "${architecture}" > "${package_root}/PLATFORM"
  (
    cd "${package_root}"
    sha256sum bin/yundu-server config/config.yaml config/env.example > MANIFEST.sha256
  )
  chmod 0755 "${package_root}/bin/yundu-server"
  tar -C "${work_dir}" -czf "${release_dir}/${package_name}.tar.gz" "${package_name}"
done

(
  cd "${release_dir}"
  sha256sum "yundu-server-v${release_version}-linux-amd64.tar.gz" "yundu-server-v${release_version}-linux-arm64.tar.gz" > "SHA256SUMS-v${release_version}.txt"
)

echo "release packages written to ${release_dir}"
