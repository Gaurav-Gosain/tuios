package junk_identifier

func foo() {} // want "the name foo says nothing"

func compute(foo2 int) int { // want "the name foo2 says nothing"
	stuff := foo2 * 2 // want "the name stuff says nothing"
	total := stuff + 1
	return total
}

// Not reported: names that only start like a stand-in word, a bar of a
// chart, and a numbered pair.

func sum(food, barrier, bar int) int { return food + barrier + bar }

func swap(val1, val2 int) (int, int) { return val2, val1 }
