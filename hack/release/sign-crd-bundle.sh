#!/usr/bin/env bash
# Sign the CRD bundle's checksum file with cosign, keyless.
#
# The checksum is what gets signed rather than the tarball itself: it already
# binds the tarball's bytes, it is the file a consumer checks the download
# against, and it keeps the signature bundle small. Verifying the signature and
# then running `sha256sum -c` covers the tarball transitively.
#
# Environment:
#   TAG               release tag, e.g. v0.4.0
#   CRD_PACKAGE_DIR   directory holding the packaged bundle (default: _crd-package)
#   GITHUB_REPOSITORY owner/repo, used to bound the identity on verification

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/release/identity.sh
. "${here}/identity.sh"

: "${TAG:?TAG must be set}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

package_dir="${CRD_PACKAGE_DIR:-_crd-package}"
checksum="${package_dir}/kubefleet-crds-${TAG}.tgz.sha256"
bundle="${checksum}.bundle"

if [ ! -f "${checksum}" ]; then
  echo "::error::No checksum file at ${checksum}; run 'make crd-package' first."
  exit 1
fi

OIDC_ISSUER="${RELEASE_OIDC_ISSUER}"
IDENTITY_PATTERN="$(release_identity_pattern "${GITHUB_REPOSITORY}")"

echo "Signing ${checksum}"
cosign sign-blob --yes --bundle "${bundle}" "${checksum}"

# Same reasoning as sign-images.sh: verify with the identity consumers will use,
# so a signature that cannot be verified fails the release rather than shipping.
cosign verify-blob \
  --bundle "${bundle}" \
  --certificate-oidc-issuer "${OIDC_ISSUER}" \
  --certificate-identity-regexp "${IDENTITY_PATTERN}" \
  "${checksum}" >/dev/null

echo "✅ ${checksum} signed and verified; bundle at ${bundle}"
