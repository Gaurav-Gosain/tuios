package commented_out_code

import "fmt"

func a(x int) int {
	// want +2 "the comment holds code"

	// fmt.Println(x)
	// x = x * 2
	return x
}

func b(err error) {
	// want +2 "the comment holds code"

	// if err != nil {
	// 	return
	// }
	fmt.Println(err)
}

// Not reported: an example in a doc comment, and prose with parentheses.

// Double returns x times 2. For example:
//
//	y := Double(3)
func Double(x int) int { return x * 2 }

func c(i, n int) int {
	// Call Close (once) when done.
	// TODO(someone): drop this
	// See https://example.com/a(b)
	fmt.Println()

	// The loop keeps this true:
	// i = i * 2
	// 0 <= i && i < n
	//	x := f(i)

	// 0 <= i && i < n
	i++

	// Have: i++
	// j0(x) = 1/sqrt(pi) * P(0, x)
	// V = HMAC(K, V)
	switch i {
	case 1:
		// fallthrough
	}

	//	x := f(i)
	return i * 2 // i = i * 2
}
