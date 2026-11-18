package narrator_comment

import "fmt"

type server struct{}

func (s *server) Start() error { return nil }

func run(items []string) error {
	counter := 0

	// want +2 "the comment says what the next line says"

	// increment the counter
	counter++

	// want +2 "says what the next line says"

	// loop over the items
	for _, item := range items {
		fmt.Println(item)
	}

	srv := &server{}

	// want +2 "says what the next line says"

	// Start the server.
	if err := srv.Start(); err != nil {
		return err
	}

	// Not reported: a reason, a long comment, a comment at the end of a
	// line, and a comment with words the code does not hold.

	// Count twice, because the first pass skips headers.
	counter++

	// The items arrive sorted by the caller, and a stable order keeps the output the same between runs.
	counter++

	counter++ // increment the counter

	// Print the result for the person who reads the log.
	fmt.Println(counter)

	// counter = counter + 1
	counter++
	return nil
}

// Count returns the counter.
func Count(counter int) int { return counter }
