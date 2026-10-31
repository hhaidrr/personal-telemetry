package ownership

import "strings"

type PathMap map[string]string

func (p PathMap) Resolve(path string) (string, bool) {
	best := -1
	var out string
	for from, to := range p {
		if strings.HasPrefix(path, from) && len(from) > best {
			best = len(from)
			out = to + path[len(from):]
		}
	}
	if best < 0 || isVendored(out) {
		return "", false
	}
	return out, true
}

func isVendored(path string) bool {
	return strings.Contains(path, "/site-packages/") || strings.Contains(path, "/node_modules/")
}
