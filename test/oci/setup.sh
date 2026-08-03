#!/usr/bin/env bash

set -euo pipefail

CONTAINER_NAME="local-registry"
IMAGE="registry:3"
PORT="9000"
USERNAME="admin"
PASSWORD="testonly"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AUTH_DIR="${SCRIPT_DIR}/auth"
HTPASSWD_FILE="${AUTH_DIR}/htpasswd"
OUTPUT_DIR="${SCRIPT_DIR}/output/."
RAW_MANIFESTS_DIR="${SCRIPT_DIR}/testdata/raw"
KUSTOMIZE_DIR="${SCRIPT_DIR}/testdata/kustomize"
REGISTRY_HOST="localhost:${PORT}"
RAW_ORAS_REF="${REGISTRY_HOST}/testdata/manifests:latest"
KUSTOMIZE_ORAS_REF="${REGISTRY_HOST}/testdata/kustomize:latest"
ORAS_ARTIFACT_TYPE="kubernetes/objects"

mkdir -p "${AUTH_DIR}"
mkdir -p "${OUTPUT_DIR}"

# Generate bcrypt htpasswd credentials for registry basic auth.
docker run --rm --entrypoint htpasswd httpd:2 -Bbn "${USERNAME}" "${PASSWORD}" > "${HTPASSWD_FILE}"

# Remove an existing container with the same name to make reruns idempotent.
if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"; then
	docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

docker run -d \
	--name "${CONTAINER_NAME}" \
	-p "${PORT}:5000" \
	-v "${AUTH_DIR}:/auth" \
	-e REGISTRY_AUTH=htpasswd \
	-e REGISTRY_AUTH_HTPASSWD_REALM="Registry Realm" \
	-e REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd \
	--restart unless-stopped \
	"${IMAGE}"

if ! command -v oras >/dev/null 2>&1; then
	echo "oras command not found; install ORAS CLI to package and push manifests" >&2
	exit 1
fi

ARTIFACT_PATHS=()
shopt -s nullglob dotglob
for path in "${RAW_MANIFESTS_DIR}"/*; do
	entry="${path##*/}"
	# Skip the EXTRAS.md file; it is not part of the artifact in the initial release.
	if [ "${entry}" = "EXTRAS.md" ]; then
		continue
	fi
	ARTIFACT_PATHS+=("${entry}")
done
shopt -u nullglob dotglob

if [ "${#ARTIFACT_PATHS[@]}" -eq 0 ]; then
	echo "no files or directories found under ${RAW_MANIFESTS_DIR}" >&2
	exit 1
fi

oras login "${REGISTRY_HOST}" \
	--username "${USERNAME}" \
	--password "${PASSWORD}" \
	--plain-http

pushd "${RAW_MANIFESTS_DIR}" >/dev/null
oras push --plain-http \
	--artifact-type "${ORAS_ARTIFACT_TYPE}" \
	"${RAW_ORAS_REF}" \
	"${ARTIFACT_PATHS[@]}"
popd >/dev/null

pushd "${KUSTOMIZE_DIR}" >/dev/null
oras push --plain-http \
	--artifact-type "${ORAS_ARTIFACT_TYPE}" \
	"${KUSTOMIZE_ORAS_REF}" \
	"base" \
	"overlays" \
	"README.md"
popd >/dev/null

echo "Local registry is running at http://localhost:${PORT}"
echo "Username: ${USERNAME}"
echo "Password: ${PASSWORD}"
echo "Pushed OCI artifact: ${RAW_ORAS_REF}"
echo "Pushed OCI artifact: ${KUSTOMIZE_ORAS_REF}"

