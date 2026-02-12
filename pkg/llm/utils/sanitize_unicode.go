package utils

func SanitizeSurrogates(text string) string {
	if text == "" {
		return ""
	}
	b := make([]rune, 0, len(text))
	var prevWasHigh bool
	for _, r := range []rune(text) {
		isHigh := r >= 0xD800 && r <= 0xDBFF
		isLow := r >= 0xDC00 && r <= 0xDFFF
		if isHigh {
			prevWasHigh = true
			continue
		}
		if isLow {
			if prevWasHigh {
				prevWasHigh = false
				continue
			}
			continue
		}
		if prevWasHigh {
			prevWasHigh = false
		}
		b = append(b, r)
	}
	return string(b)
}
