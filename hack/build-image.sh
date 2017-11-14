#!/bin/bash

set -e

OS_ROOT=$(dirname ${BASH_SOURCE})/..

# Register function to be called on EXIT to remove generated binary.
function cleanup {
  rm "${OS_ROOT}/images/origin-web-console-server/bin/origin-web-console-server"
}
trap cleanup EXIT

mkdir -p "${OS_ROOT}/images/origin-web-console-server/bin"
cp -v ${OS_ROOT}/_output/bin/origin-web-console-server "${OS_ROOT}/images/origin-web-console-server/bin/origin-web-console-server"
docker build -t openshift/origin-web-console-server:latest ${OS_ROOT}/images/origin-web-console-server