#!/usr/bin/env bash
set -euo pipefail

ssh -T runner02 'su - gitlab-runner -c "cd ~/room && git pull && ./build.sh"'
