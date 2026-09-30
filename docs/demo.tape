# The README's demo. Record it with docs/demo/record.sh, which sets up the
# stand-in agents and the demo tmux server this attaches to.
Output docs/demo.gif

Require tmux
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 15
Set Width 1200
Set Height 700
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms
Set Framerate 20
Set WindowBar Colorful
Set BorderRadius 8

Hide
Type "tmux -L hivedemo attach -t hive" Enter
Sleep 1.5s
Show

# The tree: Claude started opencode, which runs Codex headless. The preview
# follows the cursor.
Sleep 3s
Down
Sleep 2s
Down
Sleep 2.5s
Down
Sleep 2s

# Send the Copilot session a message, then jump into its pane to watch.
Type "s"
Sleep 500ms
Type "it still flakes on CI, check the timeouts"
Sleep 600ms
Enter
Sleep 2.5s
Enter
Sleep 4s

# prefix+a opens the tree over it. Find the Claude session and start a child
# of it, in a folder picked from the suggestions.
Ctrl+B
Type "a"
Sleep 1.5s
Type "/redesign"
Sleep 700ms
Enter
Sleep 700ms
Type "c"
Sleep 1.2s
Ctrl+U
Type "~/code/"
Sleep 1.5s
Type "sh"
Sleep 900ms
Tab
Sleep 1.2s
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 900ms
Enter
Sleep 500ms
Type "add a skeleton loader to the cart"
Sleep 600ms
Enter
Sleep 3s

# The new agent sits under Claude.
Ctrl+B
Type "a"
Sleep 1.5s
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 3.5s
