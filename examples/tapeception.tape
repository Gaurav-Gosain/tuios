# Plays itself recursively. Run it from the repository root with a tuios
# binary built there: go build -o tuios ./cmd/tuios

EnableTiling
NewWindow
Sleep 1s
Type "./tuios tape play ./examples/tapeception.tape"
Enter
Sleep 1s
