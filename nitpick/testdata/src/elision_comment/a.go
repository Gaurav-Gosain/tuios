package elision_comment

func a() int {
	// want +2 "stands in for code"

	// ... rest of implementation
	return 0
}

func b() error {
	// want +2 "stands in for code"

	// TODO: implement
	return nil
}

func c(x int) int {
	// want +2 "stands in for code"

	// existing code unchanged
	return x
}

// Not reported: a plan with detail, and a comment about data.

// TODO: implement retries with backoff for the 503 answer.
func d() {}

func e() error {
	// The rest of the list is sorted below.
	// Hold the lock for the rest of the function.
	// Not implemented.
	return nil
}

// readRest reads the rest of the file.
func readRest() {}

// Run shows how to call it:
//
//	func main() {
//		// ... rest of the program ...
//	}
func Run() {}
