package protoutil

import (
	"regexp"
	"sort"
	"strings"
)

// ParseProtoForTypes is a quick proto parsing helper for dropdown (top-level messages).
// It intentionally doesn't fully parse protobuf grammar – good enough for MVP UI.
var (
	rePackage = regexp.MustCompile(`(?m)^\s*package\s+([a-zA-Z0-9_.]+)\s*;`)
	reMessage = regexp.MustCompile(`(?m)^\s*message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
)

func ParseProtoForTypes(protoBytes []byte) (pkg string, msgs []string) {
	s := string(protoBytes)
	if m := rePackage.FindStringSubmatch(s); len(m) == 2 {
		pkg = strings.TrimSpace(m[1])
	}
	all := reMessage.FindAllStringSubmatch(s, -1)
	seen := map[string]bool{}
	for _, m := range all {
		if len(m) == 2 {
			name := m[1]
			if !seen[name] {
				seen[name] = true
				msgs = append(msgs, name)
			}
		}
	}
	sort.Strings(msgs)
	return pkg, msgs
}
