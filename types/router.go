package types

import "regexp"

var (
	// IsAlphaNumeric defines a regular expression for matching against alpha-numeric
	// values.
	IsAlphaNumeric = regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString

	// IsAlphaNumericOrUnderscore [AGORIC] is a regular expression that matches all typical
	// JSON property names.
	IsAlphaNumericOrUnderscore = regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString
)
