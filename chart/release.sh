#!/bin/bash

pushd repo
helm package ..
helm repo index .
popd
