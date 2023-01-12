package extproc

func StrInSlice(s []string, r string) bool {
	for _, v := range s {
		if v == r {
			return true
		}
	}
	return false
}
