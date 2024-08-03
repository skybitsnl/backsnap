#!/bin/bash

TAG_SOURCE="sjorsgielen/backsnap:latest"

PUSH="0"
TAG_DESTINATION="$1"
if [ "$TAG_DESTINATION" == "--push" ]; then
	PUSH="1"
	TAG_DESTINATION="$2"
fi

if [ -z "$TAG_DESTINATION" ]; then
	echo "Usage: $0 [ --push ] <tag destination>"
	echo "Example: $0 my-private-registry/backsnap:test-new-feature"
	echo ""
	echo "This will tag the recently built -arm64 and -amd64 images towards your registry,"
	echo "and also create a multiarch image with the exact name you provided."
	echo "If you pass --push, it will also push all images to your registry."
	exit 1
fi

set -xe

docker tag ${TAG_SOURCE}-arm64 ${TAG_DESTINATION}-arm64
docker tag ${TAG_SOURCE}-amd64 ${TAG_DESTINATION}-amd64

# The images must be pushed before we can create the manifest

{ set +x; } 2>/dev/null
if [ "$PUSH" = "1" ]; then
	set -x
	docker push ${TAG_DESTINATION}-arm64
	docker push ${TAG_DESTINATION}-amd64
else
	set -x
fi

# need to remove explicitly, or 'create' doesn't seem to refresh properly
docker manifest rm ${TAG_DESTINATION} 2>/dev/null || true

docker manifest create ${TAG_DESTINATION} \
	--amend ${TAG_DESTINATION}-arm64 \
	--amend ${TAG_DESTINATION}-amd64

{ set +x; } 2>/dev/null
if [ "$PUSH" = "1" ]; then
	set -x
	docker manifest push ${TAG_DESTINATION}
fi
