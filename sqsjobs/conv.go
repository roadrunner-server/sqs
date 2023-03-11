package sqsjobs

func convAttr(h map[string]string) map[string][]string {
	ret := make(map[string][]string, len(h))

	for k := range h {
		ret[k] = []string{
			h[k],
		}
	}

	return ret
}
