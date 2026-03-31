Output docs/demos/gifs/watch.gif

Set Shell "zsh"
Set FontSize 18
Set Width 1200
Set Height 760
Set Padding 24
Set TypingSpeed 40ms

Type "./docs/demos/scripts/setup-demo.sh >/tmp/mainline-demo-repo.txt"
Enter
Sleep 1s

Type "cd $(cat /tmp/mainline-demo-repo.txt)"
Enter
Sleep 500ms

Type "MQ=/Users/devrel/Projects/recallnet/mainline/bin/mq"
Enter
Sleep 500ms

Type "$MQ submit --repo ../feature-login >/dev/null && $MQ run-once --repo . >/dev/null && $MQ publish --repo . >/dev/null"
Enter
Sleep 2s

Type "$MQ watch --repo . --max-cycles 1"
Enter
Sleep 3s
