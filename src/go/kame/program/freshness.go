package program

func isPath(value string) bool {
	return len(value) != 0 && (value[0] == '/' || (len(value) > 1 && value[0] == '.' && value[1] == '/'))
}
func isFileName(value string) bool { return isPath(value) || hasSlash(value) }
func hasSlash(value string) bool {
	for i := range value {
		if value[i] == '/' {
			return true
		}
	}
	return false
}
