#!/usr/bin/env bash
# Exercise the release scripts against a stubbed gh CLI.
#
# These scripts decide whether a release goes public, so their failure modes
# matter more than most: the cases below are the ones where getting it wrong
# publishes something wrong rather than just failing the run.
#
# Run directly: ./hack/release/test-release-scripts.sh
# Requires: bash, jq. No test framework.

set -uo pipefail

cd "$(dirname "$0")" || exit 1
here="${PWD}"
export PATH="${here}/testdata:${PATH}"

create_script="${here}/create-draft-release.sh"
publish_script="${here}/publish-release.sh"
sign_images_script="${here}/sign-images.sh"
sign_bundle_script="${here}/sign-crd-bundle.sh"

# The same definition the signing scripts use, so the assertions below are
# about the scripts' real behaviour rather than about a copy of it.
# shellcheck source=hack/release/identity.sh
. "${here}/identity.sh"
identity_regexp="$(release_identity_pattern kubefleet-dev/kubefleet)"

# The issuer, unlike the pattern, is written out here as a literal rather than
# read from identity.sh. Sourcing it would make this agree with whatever the
# scripts say, including a swap to an attacker's issuer - the assertion would
# move with the thing it is supposed to pin. GitHub's issuer is a fixed public
# constant, so hard-coding it is what gives the check its value.
github_oidc_issuer="https://token.actions.githubusercontent.com"

passed=0
failed=0

# Every case runs one script with a fresh stub log, then asserts on its exit
# code, its output, and the gh commands it issued.
run_case() {
  FAKE_GH_LOG="$(mktemp)"
  export FAKE_GH_LOG
  output=""
  rc=0
  # stdin from /dev/null: these scripts are meant to run unattended, and a
  # command that unexpectedly reads stdin should fail the case rather than
  # block forever on the terminal of whoever ran the suite by hand.
  output="$(env "$@" </dev/null 2>&1)" || rc=$?
  log="$(cat "${FAKE_GH_LOG}")"
  rm -f "${FAKE_GH_LOG}"
}

ok() {
  echo "  PASS  $1"
  passed=$((passed + 1))
}

bad() {
  echo "  FAIL  $1"
  echo "        rc=${rc}"
  echo "        output: ${output}"
  echo "        gh calls: $(tr '\n' '|' <<<"${log}")"
  failed=$((failed + 1))
}

expect_rc() { # expect_rc <want> <description>
  if [ "${rc}" = "$1" ]; then ok "$2 (rc=${rc})"; else bad "$2 - want rc=$1"; fi
}

expect_gh() { # expect_gh <substring> <description>
  if grep -qF -- "$1" <<<"${log}"; then ok "$2"; else bad "$2 - no gh call matching '$1'"; fi
}

expect_no_gh() { # expect_no_gh <substring> <description>
  if grep -qF -- "$1" <<<"${log}"; then bad "$2 - unexpected gh call '$1'"; else ok "$2"; fi
}

expect_output() { # expect_output <substring> <description>
  if grep -qF -- "$1" <<<"${output}"; then ok "$2"; else bad "$2 - output lacks '$1'"; fi
}

echo "== signing identity constants =="

if [ "${RELEASE_OIDC_ISSUER}" = "${github_oidc_issuer}" ]; then
  ok "identity.sh pins GitHub's OIDC issuer"
else
  rc="n/a"; output="identity.sh has RELEASE_OIDC_ISSUER=${RELEASE_OIDC_ISSUER}"; log=""
  bad "identity.sh pins GitHub's OIDC issuer"
fi

echo "== check-signing-ref.sh =="

# This runs before anything is published, so its whole value is that it agrees
# with what cosign will decide much later. Same pattern, opposite consequence:
# here a mismatch costs nothing, at signing time it costs a published image.
check_ref_script="${here}/check-signing-ref.sh"
check_ref() { # check_ref <want_rc> <ref> <description>
  run_case GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
    GITHUB_WORKFLOW_REF="kubefleet-dev/kubefleet/.github/workflows/release.yml@$2" \
    bash "${check_ref_script}"
  expect_rc "$1" "$3"
}

check_ref 0 refs/tags/v0.4.0 "a tag push can sign"
check_ref 0 refs/tags/v0.4.0-rc.1 "a release-candidate tag can sign"
check_ref 0 refs/heads/main "a dispatch from the default branch can sign"
check_ref 0 refs/heads/release-0.4 "a dispatch from a release branch can sign"
check_ref 1 refs/heads/some-feature-branch "a dispatch from an arbitrary branch is refused"
check_ref 1 refs/tags/nonsense "a non-semver tag is refused"
expect_output "Nothing has been published" "refusal says nothing was published"

# -u because env does not clear what it inherits, and GITHUB_WORKFLOW_REF is
# always set inside Actions: without it this case would take the "present but
# untrusted" branch in CI and never reach the guard it is meant to cover.
run_case -u GITHUB_WORKFLOW_REF GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  bash "${check_ref_script}"
expect_rc 1 "missing GITHUB_WORKFLOW_REF: fails rather than assuming trusted"
expect_output "GITHUB_WORKFLOW_REF" "missing GITHUB_WORKFLOW_REF: names the variable"

echo "== create-draft-release.sh =="

run_case FAKE_GH_STATE=absent TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 0 "no existing release, stable: succeeds"
expect_gh "gh release create v0.4.0 --title v0.4.0 --generate-notes --draft --verify-tag" \
  "no existing release, stable: creates a verified draft"
expect_no_gh "--prerelease" "stable release is not flagged as a pre-release"

run_case FAKE_GH_STATE=absent TAG=v0.4.0-rc.1 PRERELEASE=true bash "${create_script}"
expect_rc 0 "no existing release, RC: succeeds"
expect_gh "--draft --verify-tag --prerelease" "RC is created as a pre-release"

run_case FAKE_GH_STATE=draft TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 0 "existing draft: succeeds"
expect_no_gh "release create" "existing draft is reused, not recreated"

run_case FAKE_GH_STATE=published TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 1 "already-published release: refuses"
expect_output "::error::" "already-published release: annotates the failure"
expect_no_gh "release create" "already-published release: creates nothing"

# A GitHub outage must not be read as "the release does not exist" - that would
# take the create path and step over the published-release guard above.
run_case FAKE_GH_STATE=absent FAKE_GH_ERROR="HTTP 503: Service unavailable" \
  TAG=v0.4.0 PRERELEASE=false bash "${create_script}"
expect_rc 1 "API error that is not a 404: fails closed"
expect_no_gh "release create" "API error: creates nothing"

echo "== publish-release.sh =="

all_uploaded="$(printf 'kubefleet-crds-v0.4.0.tgz;uploaded;4096\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="${all_uploaded}" TAG=v0.4.0 PRERELEASE=false \
  bash "${publish_script}"
expect_rc 0 "complete draft, stable: publishes"
expect_gh "gh release edit v0.4.0 --draft=false --prerelease=false" \
  "stable release is published without the pre-release flag"

run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0-rc.1.tgz;uploaded;4096\nkubefleet-crds-v0.4.0-rc.1.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0-rc.1.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0-rc.1 PRERELEASE=true bash "${publish_script}"
expect_rc 0 "complete draft, RC: publishes"
# A draft created by hand defaults to prerelease=false, so the flag has to be
# set at publish time or an RC becomes the repository's "Latest release".
expect_gh "--draft=false --prerelease=true" "RC is published flagged as a pre-release"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="kubefleet-crds-v0.4.0.tgz;uploaded;4096" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "missing checksum asset: refuses to publish"
expect_no_gh "release edit" "missing checksum asset: release stays a draft"

run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="" TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "no assets at all: refuses to publish"

# GitHub keeps an asset row for an upload that never finished; it is present by
# name but not in the "uploaded" state.
run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz;new;0\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "interrupted upload (state != uploaded): refuses to publish"
expect_no_gh "release edit" "interrupted upload: release stays a draft"

run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz;uploaded;0\nkubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.sha256.bundle;uploaded;2048')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "zero-byte asset: refuses to publish"

# Names alone are not proof; the bundle ships a checksum, so it gets checked.
run_case FAKE_GH_STATE=draft FAKE_GH_ASSETS="${all_uploaded}" FAKE_GH_DOWNLOAD=corrupt \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "bundle that fails its own checksum: refuses to publish"
expect_no_gh "release edit" "failed checksum: release stays a draft"

# An asset whose name only looks right must not satisfy the check.
run_case FAKE_GH_STATE=draft \
  FAKE_GH_ASSETS="$(printf 'kubefleet-crds-v0.4.0.tgz.sha256;uploaded;98\nkubefleet-crds-v0.4.0.tgz.asc;uploaded;800')" \
  TAG=v0.4.0 PRERELEASE=false bash "${publish_script}"
expect_rc 1 "similar-but-wrong asset names: refuses to publish"

echo "== sign-images.sh =="

# buildx writes one metadata file per image; "containerimage.digest" in it is
# the only record of what this run actually pushed.
write_metadata() { # write_metadata <dir> <image> <digest|"">
  mkdir -p "$1"
  if [ -n "$3" ]; then
    printf '{"containerimage.digest":"%s","image.name":"ghcr.io/x/%s"}\n' "$3" "$2" >"$1/$2.json"
  else
    printf '{"image.name":"ghcr.io/x/%s"}\n' "$2" >"$1/$2.json"
  fi
}

hub_digest="sha256:1111111111111111111111111111111111111111111111111111111111111111"
member_digest="sha256:2222222222222222222222222222222222222222222222222222222222222222"
token_digest="sha256:3333333333333333333333333333333333333333333333333333333333333333"

meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent "${hub_digest}"
write_metadata "${meta_dir}" member-agent "${member_digest}"
write_metadata "${meta_dir}" refresh-token "${token_digest}"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 0 "metadata for every built image: signs successfully"
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/hub-agent@${hub_digest}" \
  "signs the hub-agent digest this run pushed"
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/member-agent@${member_digest}" \
  "signs the member-agent digest this run pushed"
# Driving the loop off the metadata directory is what makes "everything built
# gets signed" structural: a hand-maintained list could drop this one silently.
expect_gh "cosign sign --recursive --yes ghcr.io/kubefleet-dev/kubefleet/refresh-token@${token_digest}" \
  "signs every image present in the metadata directory"
# Signing a tag would sign whatever it resolves to at signing time, not the
# bytes this run built.
expect_no_gh "ghcr.io/kubefleet-dev/kubefleet/hub-agent:" "never signs by tag"
expect_gh "cosign verify" "verifies each signature it creates"
# Which identity it verifies against is the point. Without this, every
# assertion above would still pass against a script that accepted any signer.
expect_gh "--certificate-identity-regexp ${identity_regexp}" \
  "verifies images against the shared ref-constrained identity"
# The identity is only a string; any OIDC provider could mint a certificate
# carrying it. Pinning the issuer is what makes the pattern mean anything, so
# an unpinned issuer has to fail the suite rather than pass unnoticed.
expect_gh "--certificate-oidc-issuer ${github_oidc_issuer}" \
  "binds image verification to GitHub's OIDC issuer"
rm -rf "${meta_dir}"

meta_dir="$(mktemp -d)"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 1 "no metadata at all: fails rather than signing nothing"
expect_no_gh "cosign sign" "no metadata: signs nothing"
rm -rf "${meta_dir}"

meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent ""
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet bash "${sign_images_script}"
expect_rc 1 "metadata without a digest: fails rather than signing something else"
expect_no_gh "cosign sign" "no digest: signs nothing"
rm -rf "${meta_dir}"

# An unverifiable signature is worse than none, because the docs tell users to
# rely on it - so a failed verification has to fail the release.
meta_dir="$(mktemp -d)"
write_metadata "${meta_dir}" hub-agent "${hub_digest}"
run_case REGISTRY=ghcr.io/kubefleet-dev/kubefleet IMAGE_METADATA_DIR="${meta_dir}" \
  GITHUB_REPOSITORY=kubefleet-dev/kubefleet FAKE_COSIGN_FAIL=verify \
  bash "${sign_images_script}"
expect_rc 1 "signature that will not verify: fails the release"
rm -rf "${meta_dir}"

echo "== signing identity pattern =="

# The identity regexp is half the security value of keyless signing - the issuer
# asserted above is the other half - and it is what a consumer pins to. These
# exercise the definition the signing scripts actually use (sourced at the top of
# this file) rather than a copy of it: a copy would keep passing after the
# scripts had drifted, which is the one failure this suite most needs to catch.
#
# cosign matches with Go's RE2; this uses ERE. They are not equivalent: grep
# anchors per line where Go anchors at start and end of text, so grep is the
# more permissive of the two. Every `reject` below therefore holds a fortiori
# under RE2, and every `accept` is valid because these inputs are single-line.
# Anything relying on a construct where the two differ needs checking against Go.
pattern="${identity_regexp}"
check_identity() { # check_identity <accept|reject> <san> <description>
  if grep -qE "${pattern}" <<<"$2"; then result=accept; else result=reject; fi
  if [ "${result}" = "$1" ]; then ok "$3"; else
    rc="n/a"; output="${result} for $2"; log=""; bad "$3"
  fi
}

workflow="https://github.com/kubefleet-dev/kubefleet/.github/workflows/release.yml"

check_identity accept "${workflow}@refs/tags/v0.4.0" \
  "accepts a stable tag-triggered release signature"
check_identity accept "${workflow}@refs/tags/v0.4.0-rc.1" \
  "accepts a release-candidate tag"
check_identity accept "${workflow}@refs/tags/v10.20.30" \
  "accepts multi-digit version components"
# A workflow_dispatch runs the workflow definition from the ref it was started
# on, and RELEASING.md documents dispatching from the default branch.
check_identity accept "${workflow}@refs/heads/main" \
  "accepts a dispatch from the default branch"
check_identity accept "${workflow}@refs/heads/release-0.4" \
  "accepts a dispatch from a release branch"

# The reason the ref is constrained at all: a dispatch from an unprotected
# branch would otherwise run a modified release.yml under a trusted identity.
check_identity reject "${workflow}@refs/heads/attacker-branch" \
  "rejects a dispatch from an arbitrary branch"
check_identity reject "${workflow}@refs/heads/main-evil" \
  "rejects a branch that merely starts with the default branch name"
check_identity reject "${workflow}@refs/heads/release-0.4-evil" \
  "rejects a release branch with a suffix"
check_identity reject "${workflow}@refs/tags/v0.4.0-evil" \
  "rejects a tag with a non-release-candidate suffix"
check_identity reject "${workflow}@refs/tags/notatag" \
  "rejects a tag that is not semver-shaped"
check_identity reject "${workflow}@refs/tags/v0.4.0/extra" \
  "rejects anything appended after a valid tag"
check_identity reject "prefix-${workflow}@refs/tags/v0.4.0" \
  "rejects a trusted identity embedded in a longer string"
check_identity reject \
  "https://github.com/kubefleet-dev/kubefleet/.github/workflows/squad-docs.yml@refs/heads/main" \
  "rejects another workflow in the same repository"
check_identity reject "${workflow}.bak@refs/tags/v0.4.0" \
  "rejects a filename that merely starts with release.yml"
check_identity reject \
  "https://github.com/evil/kubefleet/.github/workflows/release.yml@refs/tags/v0.4.0" \
  "rejects the same workflow path in a different repository"

# RELEASING.md hands consumers a verification command containing this pattern
# verbatim. If it drifts, users pin an identity the release no longer signs
# under and every verification fails - the kind of break that only shows up in
# someone else's terminal, long after the release shipped.
#
# Counted, not grepped: the pattern appears in more than one code block, and a
# first-match test would pass while a maintainer updated one and missed the
# other - which is the realistic way this drifts.
releasing_md="${here}/../../RELEASING.md"
if [ ! -f "${releasing_md}" ]; then
  rc="n/a"; output="no RELEASING.md at ${releasing_md}"; log=""
  bad "RELEASING.md documents the pattern the scripts use"
else
  current="$(grep -cF -- "${identity_regexp}" "${releasing_md}")"
  total="$(grep -c -- '--certificate-identity-regexp' "${releasing_md}")"
  if [ "${current}" -gt 0 ] && [ "${current}" = "${total}" ]; then
    ok "every documented identity in RELEASING.md is the one the scripts use (${current})"
  else
    rc="n/a"; log=""
    output="${current} of ${total} --certificate-identity-regexp occurrences match ${identity_regexp}"
    bad "every documented identity in RELEASING.md is the one the scripts use"
  fi
fi

# The tag grammar is spelled independently in setup-release.yml, which gates the
# whole release. Deliberately one-way: the dangerous direction is that file
# widening - say to accept -beta.N - while identity.sh does not, because then a
# tag passes validation, gets built and pushed, and only fails at signing. The
# reverse is harmless, since setup-release.yml rejects the tag before anything
# is published. Compared literally against the grammar identity.sh exports, so
# there is no hand-escaped third copy to re-escape on a legitimate change.
#
# A literal comparison only catches widenings that disturb the mirrored
# fragment. Appending a second optional group would leave it intact and pass
# here; check-signing-ref.sh is the backstop for that, and it fails the release
# at the gate rather than after publishing.
setup_yml="${here}/../../.github/workflows/setup-release.yml"
if [ ! -f "${setup_yml}" ]; then
  rc="n/a"; output="no setup-release.yml at ${setup_yml}"; log=""
  bad "setup-release.yml validates no tag the signing identity would reject"
elif grep -qF -- "${RELEASE_TAG_GRAMMAR}" "${setup_yml}"; then
  ok "setup-release.yml validates no tag the signing identity would reject"
else
  rc="n/a"; log=""
  output="setup-release.yml no longer spells the grammar identity.sh exports: ${RELEASE_TAG_GRAMMAR}"
  bad "setup-release.yml validates no tag the signing identity would reject"
fi

# Repository names may contain dots; unescaped they would match any character,
# so a lookalike repository would verify. Asserted behaviourally rather than by
# inspecting the string: what matters is what the pattern accepts.
dotted_pattern="$(release_identity_pattern org/weird.name)"
dotted_workflow="https://github.com/org/weird.name/.github/workflows/release.yml"
if grep -qE "${dotted_pattern}" <<<"${dotted_workflow}@refs/tags/v0.4.0"; then
  ok "a dotted repository name still matches itself"
else
  rc="n/a"; output="${dotted_pattern}"; log=""; bad "a dotted repository name still matches itself"
fi
if grep -qE "${dotted_pattern}" <<<"https://github.com/org/weirdXname/.github/workflows/release.yml@refs/tags/v0.4.0"; then
  rc="n/a"; output="${dotted_pattern}"; log=""
  bad "an unescaped dot would match a lookalike repository"
else
  ok "escapes dots so a lookalike repository does not match"
fi

echo "== sign-crd-bundle.sh =="

pkg_dir="$(mktemp -d)"
printf 'abc123  kubefleet-crds-v0.4.0.tgz\n' >"${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  bash "${sign_bundle_script}"
expect_rc 0 "checksum present: signs successfully"
expect_gh "cosign sign-blob --yes --bundle ${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256.bundle" \
  "writes the signature bundle next to the checksum"
expect_gh "cosign verify-blob" "verifies the blob signature it just created"
expect_gh "--certificate-identity-regexp ${identity_regexp}" \
  "verifies the CRD bundle against the shared ref-constrained identity"
expect_gh "--certificate-oidc-issuer ${github_oidc_issuer}" \
  "binds CRD bundle verification to GitHub's OIDC issuer"
if [ -f "${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256.bundle" ]; then
  ok "signature bundle exists for upload"
else
  bad "signature bundle was not produced"
fi
rm -rf "${pkg_dir}"

pkg_dir="$(mktemp -d)"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  bash "${sign_bundle_script}"
expect_rc 1 "checksum missing: fails instead of signing nothing"
expect_no_gh "cosign" "checksum missing: does not invoke cosign at all"
rm -rf "${pkg_dir}"

pkg_dir="$(mktemp -d)"
printf 'abc123  kubefleet-crds-v0.4.0.tgz\n' >"${pkg_dir}/kubefleet-crds-v0.4.0.tgz.sha256"
run_case TAG=v0.4.0 CRD_PACKAGE_DIR="${pkg_dir}" GITHUB_REPOSITORY=kubefleet-dev/kubefleet \
  FAKE_COSIGN_FAIL=verify-blob bash "${sign_bundle_script}"
expect_rc 1 "blob signature that will not verify: fails the release"
rm -rf "${pkg_dir}"

echo
echo "passed=${passed} failed=${failed}"
[ "${failed}" -eq 0 ]
