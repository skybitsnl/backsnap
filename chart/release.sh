#!/bin/bash
set -e

# Index only the new chart, and merge the existing index into it, so that the
# earlier charts keep their created dates
NEW="$(mktemp -d)"
helm package . --destination "$NEW"
helm repo index "$NEW" --merge ../docs/index.yaml
mv "$NEW"/*.tgz "$NEW"/index.yaml ../docs/
rmdir "$NEW"
