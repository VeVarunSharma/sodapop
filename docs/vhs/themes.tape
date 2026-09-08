Source common.tape
Set Width 1040
Set Height 650
Set FontSize 20
Output output/themes.gif
Require sodapop-recording

Hide
Type "cd soda-shop && exec sodapop-recording --reduced-motion"
Enter
Wait+Screen@10s /chat>/
Wait+Screen@10s /1 changed/
Show
Sleep 1s
Type "/theme"
Enter
Wait+Screen@3s /APPEARANCE/
Sleep 1400ms
Down
Enter
Wait+Screen@3s /Appearance updated: graphite/
Escape
Sleep 1600ms

Type "/theme light"
Enter
Wait+Screen@3s /Appearance updated: light/
Sleep 700ms
Screenshot output/themes.png
Sleep 1400ms

Type "/theme arcade"
Enter
Wait+Screen@3s /Appearance updated: arcade/
Sleep 1800ms
Hide
Type "/exit"
Enter
Sleep 300ms
