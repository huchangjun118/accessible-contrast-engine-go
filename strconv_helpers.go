package contrast

import "strconv"

func itoa(n int) string { return strconv.Itoa(n) }

// formatFloat renders scores compactly for error messages.
func formatFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
