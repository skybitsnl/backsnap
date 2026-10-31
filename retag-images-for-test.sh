#!/bin/bash

# The images built by `goreleaser release --snapshot --clean`
IMAGES="backsnap backsnap-restic"
SOURCE_REGISTRY="sjorsgielen"
SOURCE_TAG="latest"

# The operator Deployment to patch with --deploy
NAMESPACE="${NAMESPACE:-backsnap}"
DEPLOYMENT="${DEPLOYMENT:-backsnap-operator}"

usage() {
	echo "Usage: $0 <registry> <tag> [ --deploy <kubectl context> ]"
	echo "Example: $0 my-private-registry test-new-feature --deploy my-test-cluster"
	echo ""
	echo "This will tag the recently built -arm64 and -amd64 images of the operator"
	echo "and the restic image towards your registry, push them, and also create"
	echo "and push multiarch images <registry>/backsnap:<tag> and"
	echo "<registry>/backsnap-restic:<tag>."
	echo ""
	echo "If you pass --deploy, it will also patch the operator Deployment"
	echo "${NAMESPACE}/${DEPLOYMENT} in the given kubectl context to use both images,"
	echo "and restart it. Set NAMESPACE and DEPLOYMENT to patch another Deployment."
	exit 1
}

POSITIONAL=()
CONTEXT=""
while [ $# -gt 0 ]; do
	case "$1" in
	--deploy)
		CONTEXT="$2"
		[ -n "$CONTEXT" ] || usage
		shift 2
		;;
	*)
		POSITIONAL+=("$1")
		shift
		;;
	esac
done

REGISTRY="${POSITIONAL[0]%/}"
TAG="${POSITIONAL[1]}"
if [ -z "$REGISTRY" ] || [ -z "$TAG" ] || [ ${#POSITIONAL[@]} -gt 2 ]; then
	usage
fi

KUBECTL="kubectl --context ${CONTEXT} --namespace ${NAMESPACE}"

set -xe

if [ -n "$CONTEXT" ]; then
	# Fail before pushing if the Deployment cannot be found
	$KUBECTL get deployment ${DEPLOYMENT} >/dev/null
fi

for IMAGE in $IMAGES; do
	SOURCE="${SOURCE_REGISTRY}/${IMAGE}:${SOURCE_TAG}"
	DESTINATION="${REGISTRY}/${IMAGE}:${TAG}"

	docker tag ${SOURCE}-arm64 ${DESTINATION}-arm64
	docker tag ${SOURCE}-amd64 ${DESTINATION}-amd64

	# The images must be pushed before we can create the manifest
	docker push ${DESTINATION}-arm64
	docker push ${DESTINATION}-amd64

	# need to remove explicitly, or 'create' doesn't seem to refresh properly
	docker manifest rm ${DESTINATION} 2>/dev/null || true

	docker manifest create ${DESTINATION} \
		--amend ${DESTINATION}-arm64 \
		--amend ${DESTINATION}-amd64
	docker manifest push ${DESTINATION}
done

if [ -n "$CONTEXT" ]; then
	CONTAINER="/spec/template/spec/containers/0"
	PATCH='[
		{"op": "replace", "path": "'${CONTAINER}'/image", "value": "'${REGISTRY}/backsnap:${TAG}'"},
		{"op": "add", "path": "'${CONTAINER}'/imagePullPolicy", "value": "Always"}'

	# The last -image flag wins. Only append it if it's not the last argument
	# yet, so that running this script again does not add it again.
	IMAGE_ARG="-image=${REGISTRY}/backsnap-restic:${TAG}"
	LAST_ARG=$($KUBECTL get deployment ${DEPLOYMENT} -o jsonpath='{.spec.template.spec.containers[0].args[-1:]}')
	if [ "$LAST_ARG" != "$IMAGE_ARG" ]; then
		PATCH+=',
		{"op": "add", "path": "'${CONTAINER}'/args/-", "value": "'${IMAGE_ARG}'"}'
	fi
	PATCH+=']'

	$KUBECTL patch deployment ${DEPLOYMENT} --type=json -p "$PATCH"
	# Restart, so that a new build pushed under the same tag is pulled as well
	$KUBECTL rollout restart deployment/${DEPLOYMENT}
	$KUBECTL rollout status deployment/${DEPLOYMENT} --timeout=5m
fi

{ set +x; } 2>/dev/null
echo ""
for IMAGE in $IMAGES; do
	echo "Pushed ${REGISTRY}/${IMAGE}:${TAG}"
done
if [ -n "$CONTEXT" ]; then
	echo "Deployed to ${NAMESPACE}/${DEPLOYMENT} in kubectl context ${CONTEXT}"
fi
