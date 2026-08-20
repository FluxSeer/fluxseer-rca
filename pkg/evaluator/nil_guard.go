package evaluator

// SafeString unwraps a string pointer with a default fallback to prevent nil panics.
func SafeString(val *string, fallback string) string {
	if val == nil {
		return fallback
	}
	return *val
}
