#!/usr/bin/env bash
# Sign each published image with cosign, keyless, using the workflow's GitHub
# OIDC identity.
#
# Images are signed by *digest*, read from the build metadata buildx wrote
# during this run. Signing a tag would sign whatever that tag resolves to when
# cosign runs, which is not necessarily what this run pushed - the whole point
# of a signature is to bind it to specific bytes.
#
# The set of images comes from the metadata directory rather than from a list
# passed in, so "everything this run built gets signed" is structurally true. A
# hand-maintained list could drop an image while the job still exited 0,
# publishing an unsigned image that the documentation tells users to verify.
#
# The complementary invariant - that everything a release *should* contain was
# built - is asserted by the multi-arch verification step that runs immediately
# before this script. Keep that ordering: it is what stops a dropped build
# target from being silently absent here.
#
# Environment:
#   REGISTRY            image repository prefix, e.g. ghcr.io/kubefleet-dev/kubefleet
#   IMAGE_METADATA_DIR  directory holding <image>.json from `buildx --metadata-file`
#   GITHUB_REPOSITORY   owner/repo, used to bound the identity on verification

set -euo pipefail
shopt -s nullglob

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/release/identity.sh
. "${here}/identity.sh"

: "${REGISTRY:?REGISTRY must be set}"
: "${IMAGE_METADATA_DIR:?IMAGE_METADATA_DIR must be set}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

OIDC_ISSUER="${RELEASE_OIDC_ISSUER}"
IDENTITY_PATTERN="$(release_identity_pattern "${GITHUB_REPOSITORY}")"

metadata_files=("${IMAGE_METADATA_DIR}"/*.json)
if [ "${#metadata_files[@]}" -eq 0 ]; then
  echo "::error::No build metadata in ${IMAGE_METADATA_DIR}; there is nothing to sign, which means the build did not publish what it should have."
  exit 1
fi

for metadata in "${metadata_files[@]}"; do
  image="$(basename "${metadata}" .json)"

  digest="$(jq -r '."containerimage.digest" // empty' "${metadata}")"
  case "${digest}" in
    sha256:*) ;;
    *)
      echo "::error::${metadata} has no usable containerimage.digest (got '${digest}')."
      exit 1
      ;;
  esac

  ref="${REGISTRY}/${image}@${digest}"
  echo "Signing ${ref}"
  # --recursive also signs the per-platform manifests inside the index. Without
  # it, anything that has already resolved to a platform-specific digest - some
  # admission controllers, per-arch mirroring - finds no signature.
  cosign sign --recursive --yes "${ref}"

  # Verify what was just signed. This is not ceremony: it is the only check that
  # the signature resolves against the identity consumers will verify with. A
  # signature nobody can verify is worse than no signature, because the
  # documentation tells users to rely on it.
  #
  # Retried because the realistic failure is a registry read-after-write race,
  # and re-running this job to recover would rebuild and re-push every image
  # under a new digest.
  verify_log="$(mktemp)"
  for attempt in 1 2 3; do
    if cosign verify \
      --certificate-oidc-issuer "${OIDC_ISSUER}" \
      --certificate-identity-regexp "${IDENTITY_PATTERN}" \
      "${ref}" >/dev/null 2>"${verify_log}"; then
      break
    fi
    if [ "${attempt}" = 3 ]; then
      # Print what cosign actually said: a certificate-identity mismatch is a
      # configuration fault, not a transient, and the two need different fixes.
      echo "::error::Signed ${ref} but the signature did not verify after 3 attempts: $(tr '\n' ' ' <"${verify_log}")"
      rm -f "${verify_log}"
      exit 1
    fi
    sleep 2
  done
  rm -f "${verify_log}"
  echo "✅ ${ref} signed and verified"
done
