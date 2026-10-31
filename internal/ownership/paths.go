package ownership

import (
	"path"
	"sort"
	"strings"
)

func subsystemFor(subsystems map[string][]string, p string) string {
	names := make([]string, 0, len(subsystems))
	for name := range subsystems {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, glob := range subsystems[name] {
			if globMatch(glob, p) {
				return name
			}
		}
	}
	return ""
}

func globMatch(glob, p string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(p, "/"))
}

func matchSegments(glob, p []string) bool {
	for len(glob) > 0 {
		if glob[0] == "**" {
			for i := 0; i <= len(p); i++ {
				if matchSegments(glob[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		if len(p) == 0 {
			return false
		}
		if ok, err := path.Match(glob[0], p[0]); err != nil || !ok {
			return false
		}
		glob, p = glob[1:], p[1:]
	}
	return len(p) == 0
}
