# Re-record the README demo:
#   go build -o /tmp/beadsboard . && ./docs/demo-fixture.sh && vhs docs/demo.tape
Output docs/demo.gif

Set Shell "bash"
Set Theme "TokyoNight"
Set FontSize 15
Set Width 1200
Set Height 720
Set Padding 20
Set TypingSpeed 60ms

# Start on the seeded fixture, off camera.
Hide
Type "/tmp/beadsboard --source /tmp/payments-platform"
Enter
Sleep 4s
Show
Sleep 2s

# The board: epics by priority and build order, detail tracking the cursor.
Down
Sleep 1500ms
Down
Sleep 1500ms
Up
Sleep 1s

# Drill into the epic's task list.
Right
Sleep 1s
Tab
Sleep 800ms
Tab
Sleep 800ms
Tab
Sleep 1500ms
Escape
Sleep 1s

# Fuzzy search, scoped to the list in view — here, the epic list.
Type "/"
Sleep 500ms
Type "obs"
Sleep 2s
Escape
Sleep 1s

# The attention inbox: what is waiting on you, board-wide.
Type "i"
Sleep 3s
Down
Sleep 1500ms
Enter
Sleep 2500ms

Type "q"
Sleep 1s
