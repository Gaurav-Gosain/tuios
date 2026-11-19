package i18n

// pluralCategory returns the CLDR cardinal category a whole count takes in
// lang: the form of the noun that goes with n things. Russian is the one
// language with its own rules here so far; English and every language whose
// rules are not written yet take "one" for 1 and "other" for the rest.
func pluralCategory(lang string, n int) string {
	switch lang {
	case "ru":
		// The Russian cardinal rules: a count whose last digit is 1 and
		// whose last two digits are not 11 is one, a count whose last digit
		// is 2 to 4 and whose last two digits are not 12 to 14 is few, and
		// every other count is many.
		switch {
		case n%10 == 1 && n%100 != 11:
			return "one"
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			return "few"
		default:
			return "many"
		}
	default:
		if n == 1 {
			return "one"
		}
		return "other"
	}
}
