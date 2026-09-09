Source common.tape
Output output/overview.gif
Require sodapop-recording

Hide
Type "cd soda-shop && exec sodapop-recording"
Enter
Wait+Screen@10s /chat>/
Wait+Screen@10s /1 changed/
Show
Sleep 2600ms
Screenshot output/overview.png
Sleep 200ms

Type "/"
Wait+Screen@3s /Discover commands/
Sleep 700ms
Type "di"
Sleep 1s
Tab
Sleep 900ms
Escape
Backspace@20ms 6

Shift+Tab
Wait+Screen@3s /plan>/
Sleep 1800ms
Shift+Tab
Wait+Screen@3s /auto>/
Sleep 1400ms
Shift+Tab
Wait+Screen@3s /chat>/
Sleep 600ms

Type "/diff"
Enter
Wait+Screen@5s /Pick a flavor: cola, lime, orange/
Sleep 1800ms
PageDown
Wait+Screen@5s /Served chilled/
Sleep 2300ms
Escape
Sleep 2200ms
Hide
Type "/exit"
Enter
Sleep 300ms
