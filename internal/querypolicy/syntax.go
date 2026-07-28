package querypolicy

func stripQuotedContent(value string) string {
	out := []rune(value)
	var quote rune
	escaped := false
	for index, r := range out {
		if quote == 0 {
			if r == '"' || r == '\'' || r == '`' {
				quote = r
				out[index] = ' '
			}
			continue
		}
		out[index] = ' '
		if quote != '`' && r == '\\' && !escaped {
			escaped = true
			continue
		}
		if r == quote && !escaped {
			quote = 0
		}
		escaped = false
	}
	return string(out)
}
