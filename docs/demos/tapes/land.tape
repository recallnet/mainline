Output docs/demos/gifs/land.gif

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

Type "$MQ submit --repo ../feature-login"
Enter
Sleep 2s

Type "$MQ run-once --repo ."
Enter
Sleep 2s

Type "$MQ publish --repo ."
Enter
Sleep 2s
