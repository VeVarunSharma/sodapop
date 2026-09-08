Source common.tape
Set Width 1040
Set Height 650
Set FontSize 20
Output output/diff.gif
Require sodapop-recording

Hide
Type "cd soda-shop && exec sodapop-recording --reduced-motion"
Enter
Wait+Screen@10s /chat>/
Wait+Screen@10s /1 changed/
Show
Sleep 600ms
Type "/diff staged"
Enter
Wait+Screen@5s /STAGED/
PageDown
Wait+Screen@5s /Pick a flavor: cola, lime, orange/
Sleep 2600ms
Escape
Sleep 400ms

Type "/diff unstaged"
Enter
Wait+Screen@5s /UNSTAGED/
PageDown
Wait+Screen@5s /Served chilled/
Sleep 1s
Screenshot output/diff.png
Sleep 1800ms
Escape
Sleep 400ms

Type "/diff"
Enter
Wait+Screen@5s /STAGED/
PageDown@200ms 2
Wait+Screen@5s /Served chilled/
Sleep 2200ms
Escape
Sleep 700ms
Hide
Type "/exit"
Enter
Sleep 300ms
