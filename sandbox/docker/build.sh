#!/bin/sh
# Builds the sandbox image referenced by DOCKER_IMAGE (default agentrove-sandbox:latest).
set -e
cd "$(dirname "$0")/../.."
exec docker build -f sandbox/docker/Dockerfile -t "${DOCKER_IMAGE:-agentrove-sandbox:latest}" "$@" .
