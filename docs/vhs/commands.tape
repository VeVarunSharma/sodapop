Source common.tape
Set Width 1040
Set Height 650
Set FontSize 18
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
Type "vend"
Sleep 1s
Tab
Sleep 500ms
Enter
Wait+Screen@3s /VENDING MACHINE/
Sleep 1300ms
Down
Wait+Screen@3s /Burst into ideas/
Sleep 500ms
Screenshot output/commands.png
Sleep 800ms
Escape
Sleep 500ms

Type "Outline a small Go CLI"
Shift+Tab
Wait+Screen@3s /plan>/
Sleep 1400ms
Shift+Tab
Wait+Screen@3s /auto>/
Sleep 1400ms
Shift+Tab
Wait+Screen@3s /chat>/
Sleep 1300ms
Backspace@10ms 22
Sleep 600ms
Hide
Type "/exit"
Enter
Sleep 300ms
