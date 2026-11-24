import TaskSpec

/-- Reads one stream per line on stdin (a JSON object with a start card and
its moves) and writes one line per stream: the result of each move. A line it
cannot read is answered with an error object, and the program goes on. -/
partial def loop (inp : IO.FS.Stream) (out : IO.FS.Stream) : IO Unit := do
  let line ← inp.getLine
  if line.isEmpty then return
  let line := line.trimAsciiEnd.toString
  if !line.isEmpty then
    match TaskSpec.runStream line with
    | .ok j => out.putStrLn j.compress
    | .error e => out.putStrLn (Lean.Json.mkObj [("error", e)]).compress
    out.flush
  loop inp out

def main : IO Unit := do
  loop (← IO.getStdin) (← IO.getStdout)
