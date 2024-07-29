#!/bin/bash

VERSION="$1"
APP_PATCH="$2"

if [ -z "$VERSION" -o -z "$APP_PATCH" ]; then
	echo "Usage: $0 <major>.<minor> <patch>"
	echo ""
	echo "Bumps the app version to <major>.<minor>.<patch>, and bumps the "
	echo "chart version to *at least* <major>.<minor>.<patch>, or higher."
	echo ""
	echo "Note the dot between major and minor, and the space between minor and patch."
	echo ""
	echo "For example: $0 0.3 2"
	echo "This will bump the app version to 0.3.2, and the chart version to 0.3.2,"
	echo "unless the current chart version is already 0.3.2 or newer, in which case"
	echo "it will bump to 0.3.(current patch + 1)."
	exit 1
fi

# First, copy CRDs from the repo into the chart.
cp ../config/crd/bases/*.yaml templates/crd
cp ../config/rbac/leader_election_role* templates/rbac
cp ../config/rbac/role* templates/rbac
cp ../config/rbac/service_account.yaml templates

# Replace the namespaces
find templates -type f -exec gsed -i 's/namespace: backsnap/namespace: {{ .Release.Namespace }}/' {} \;

# Figure out the versioning
CURRENT_VERSION="$(cat Chart.yaml | grep APP_VERSION: | awk '{print $3}')"
CHART_PATCH="$APP_PATCH"
if [ "$CURRENT_VERSION" = "$VERSION" ]; then
	CURRENT_CHART_PATCH="$(cat Chart.yaml | grep CHART_PATCH: | awk '{print $3}')"
	CHART_PATCH="$(($CURRENT_CHART_PATCH + 1))"
fi

# Bump version in Chart.yaml
cp Chart.template.yaml Chart.yaml
gsed -i -e "s/%APP_VERSION%/$VERSION/" -e "s/%APP_PATCH%/$APP_PATCH/" -e "s/%CHART_PATCH%/$CHART_PATCH/" Chart.yaml
