#!/bin/bash

pushd ../docs
helm package ../chart
helm repo index .
popd
