# Recorded by scripts/record-demo.sh with https://github.com/charmbracelet/vhs
Output out/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1440
Set Height 820
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set Framerate 20

Hide
Type "export PATH=/vhs:$PATH MQMON_CONFIG=/tmp/mqmon.json COLORTERM=truecolor PS1='$ ' && clear"
Enter
Sleep 500ms
Show

Type "mqmon broker -V 5"
Sleep 400ms
Enter
Sleep 5s
Screenshot out/overview.png
Sleep 1.5s

# Browse the topic tree
Tab
Tab
Sleep 500ms
Type "e"
Sleep 800ms
Down@180ms 22
Sleep 2s
Screenshot out/tree.png
Sleep 1.5s

# Filter to that topic, then clear
Enter
Sleep 2.5s
Enter
Sleep 1s

# Change subscription mid-run
Type "s"
Sleep 600ms
Type "zig"
Sleep 800ms
Screenshot out/subscribe.png
Sleep 1.5s
Tab
Sleep 600ms
Ctrl+X
Sleep 1.2s
Type "c"
Sleep 3s

# Publish
Type "p"
Sleep 500ms
Tab
Tab
Tab
Ctrl+U
Type "zigbee2mqtt/office_light/set"
Tab
Type '{"state":"ON","brightness":180}'
Sleep 1s
Screenshot out/publish.png
Sleep 1.5s
Ctrl+S
Sleep 3s

Type "q"
Sleep 500ms
