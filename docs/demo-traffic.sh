#!/bin/sh
P="mosquitto_pub -p 1883"
$P -r -t zigbee2mqtt/bridge/state -m '{"state":"online"}'
$P -r -t zigbee2mqtt/bridge/info -m '{"version":"1.40.2","coordinator":{"type":"zStack3x0","meta":{"revision":20230507}},"network":{"channel":11,"pan_id":6754},"permit_join":false}'
$P -r -t homeassistant/status -m online
$P -r -t home/office/light/state -m '{"state":"OFF","brightness":0}'
i=0
while true; do
  i=$((i+1))
  $P -t home/kitchen/temperature -m "2$((i%3)).$((i*7%10))"
  $P -t home/kitchen/humidity -m "4$((i%6))"
  $P -t home/hall/motion -m "$([ $((i%5)) = 0 ] && echo ON || echo OFF)"
  $P -t zigbee2mqtt/living_room_sensor -m "{\"battery\":$((90-i%5)),\"humidity\":4$((i%9)).2,\"linkquality\":$((120+i*13%60)),\"temperature\":2$((i%4)).$((i%10)),\"voltage\":2995}"
  $P -t zigbee2mqtt/front_door -m "{\"contact\":$([ $((i%7)) = 0 ] && echo false || echo true),\"battery\":100,\"linkquality\":$((80+i%40))}"
  $P -t energy/meter/power -m "$((300+i*37%900))"
  $P -t energy/meter/total_kwh -m "1234$((i/10)).$((i%10))"
  [ $((i%3)) = 0 ] && $P -t garden/weather -m "{\"wind_kph\":$((i%25)),\"rain_mm\":0.$((i%9)),\"uv\":$((i%8))}"
  sleep 0.7
done
