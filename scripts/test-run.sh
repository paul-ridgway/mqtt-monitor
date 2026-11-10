#!/bin/bash
# Builds mqmon and runs it against a throwaway Mosquitto broker in Docker that
# publishes some sample traffic. The broker is removed when mqmon exits.
# Extra arguments are passed through to mqmon.
set -e
cd "$(dirname "$0")/.."
./scripts/build.sh

name="mqmon-test-run-$$"
port="${MQMON_TEST_PORT:-18883}"
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run -d --rm --name "$name" -p "127.0.0.1:$port:1883" eclipse-mosquitto:2 \
  mosquitto -c /mosquitto-no-auth.conf >/dev/null
docker exec -d "$name" sh -c '
  i=0
  mosquitto_pub -t home/info -r -m "{\"name\":\"test broker\",\"rooms\":[\"kitchen\",\"hall\"]}"
  while true; do
    i=$((i+1))
    mosquitto_pub -t home/kitchen/temp -m "2$((i%10)).$((i%7))"
    mosquitto_pub -t home/hall/motion -m "$([ $((i%4)) = 0 ] && echo ON || echo OFF)"
    mosquitto_pub -t devices/lamp/state -m "{\"on\":true,\"brightness\":$((i*7%255))}"
    sleep 1
  done'

MQMON_CONFIG="$(mktemp -u -t mqmon-test-config.XXXXXX)" ./bin/mqmon --no-save "localhost:$port" "$@"
