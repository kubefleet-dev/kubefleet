#!/usr/bin/env bash
# Fail before anything is published if this run cannot produce a signature
# consumers will accept.
#
# Signing happens after the images are pushed, so without this check a release
# started from the wrong ref publishes three images, signs them, writes an
# immutable Rekor entry, and only then fails on verification - leaving public
# artifacts behind that no release references and a tag that can no longer be
# safely reused.
#
# What Fulcio actually puts in the SAN is job_workflow_ref: the ref of the
# workflow file defining the *job* that signs. That equals GITHUB_WORKFLOW_REF
# only while the signing jobs are defined inline in release.yml, which they are
# today. Move them into a reusable workflow - release.yml already calls one for
# setup - and cosign would see that file's ref instead, breaking verification
# while this check still passed. Anyone doing that has to revisit both this
# script and the pattern in identity.sh.
#
# Environment:
#   GITHUB_REPOSITORY   owner/repo
#   GITHUB_WORKFLOW_REF owner/repo/.github/workflows/release.yml@refs/...

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/release/identity.sh
. "${here}/identity.sh"

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
: "${GITHUB_WORKFLOW_REF:?GITHUB_WORKFLOW_REF must be set}"

pattern="$(release_identity_pattern "${GITHUB_REPOSITORY}")"
identity="https://github.com/${GITHUB_WORKFLOW_REF}"

# grep's ERE is marginally more permissive than the RE2 cosign will use - it
# anchors per line where Go anchors at end of text - so in principle this could
# accept something cosign later rejects. Git refnames cannot contain newlines,
# which is the only construct where the two differ here.
if ! grep -qE "${pattern}" <<<"${identity}"; then
  echo "::error::This run would sign as '${identity}', which is not an identity a release signs under. Push a vX.Y.Z tag, or dispatch from main, a release-X.Y branch, or the tag itself. Nothing has been published."
  exit 1
fi

echo "✅ ${identity} is a trusted release identity"
