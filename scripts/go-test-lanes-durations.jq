# Reads go test -json output and prints "PACKAGE TEST SECONDS" for each
# top-level test, for scripts/go-test-lanes.durations. The time is from the
# test's run event to its last event, so a test whose subtests run in parallel
# counts the wall time they took, which its own Elapsed leaves out.
def seconds: (.[0:19] + "Z" | fromdate) + ((.[19:] | capture("^(?<f>[.][0-9]+)").f // "0") | tonumber);
[.[] | select(.Test != null and (.Test | contains("/") | not) and (.Action == "run" or .Action == "pass" or .Action == "fail" or .Action == "skip"))]
| group_by(.Package + " " + .Test)[]
| "\(.[0].Package) \(.[0].Test) \((map(.Time | seconds) | max) - (map(.Time | seconds) | min) | . * 100 | round / 100)"
