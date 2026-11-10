#!/bin/bash
# Re-records demo.gif and the screenshots in docs/ using VHS in Docker,
# against a throwaway Mosquitto broker publishing sample traffic.
set -e
cd "$(dirname "$0")/.."

work="$(mktemp -d -t mqmon-demo.XXXXXX)"
net="mqmon-demo-$$"
broker="mqmon-demo-broker-$$"
cleanup() {
  docker rm -f "$broker" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  docker run --rm -v "$work:/w" alpine rm -rf /w/out >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

CGO_ENABLED=0 go build -o "$work/mqmon" ./cmd/mqmon
cp docs/demo.tape "$work/"
mkdir -p "$work/out"
chmod 777 "$work/out"

docker network create "$net" >/dev/null
docker run -d --rm --name "$broker" --network "$net" --network-alias broker \
  eclipse-mosquitto:2 mosquitto -c /mosquitto-no-auth.conf >/dev/null
docker cp docs/demo-traffic.sh "$broker:/traffic.sh"
docker exec -d "$broker" sh /traffic.sh
sleep 2

docker run --rm --network "$net" -v "$work:/vhs" ghcr.io/charmbracelet/vhs demo.tape

cp "$work/out/demo.gif" demo.gif
for f in overview tree subscribe publish; do
  cp "$work/out/$f.png" "docs/$f.png"
done
echo "Updated demo.gif and docs/*.png"
