package store

func placeholders(count int) string {
	if count < 1 {
		return ""
	}
	result := "?"
	for i := 1; i < count; i++ {
		result += ",?"
	}
	return result
}
