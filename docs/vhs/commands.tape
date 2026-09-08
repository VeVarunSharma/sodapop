Source common.tape
Set Width 1040
Set Height 650
Set FontSize 20
Output output/commands.gif
Require sodapop-recording

Hide
Type "cd soda-shop && exec sodapop-recording --reduced-motion"
Enter
Wait+Screen@10s /chat>/
Wait+Screen@10s /1 changed/
Show
Sleep 600ms
Type "/"
Wait+Screen@3s /Discover commands/
Sleep 1200ms
Type "pl"
Sleep 1s
Screenshot output/commands.png
Sleep 300ms
Tab
Sleep 900ms
Escape
Backspace@20ms 6

Shift+Tab
Wait+Screen@3s /plan>/
Type "Outline a small Go CLI"
Sleep 2s
Shift+Tab
Wait+Screen@3s /chat>/
Sleep 1300ms
Backspace@10ms 22
Sleep 600ms
Hide
Type "/exit"
Enter
Sleep 300ms
