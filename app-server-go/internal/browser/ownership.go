package browser

func ownsTarget(targets []string, id string) bool {
	for _, target := range targets {
		if target == id {
			return true
		}
	}
	return false
}
