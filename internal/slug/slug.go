package slug

import (
	"sort"
	"strconv"
	"strings"
)

type PR struct {
	Owner  string
	Repo   string
	Number int
	Fork   bool
}

func Assign(prs []PR) []string {
	groups := map[string][]int{}
	for i, p := range prs {
		k := strings.ToLower(p.Repo) + "#" + strconv.Itoa(p.Number)
		groups[k] = append(groups[k], i)
	}
	out := make([]string, len(prs))
	for _, idx := range groups {
		sort.Slice(idx, func(a, b int) bool {
			pa, pb := prs[idx[a]], prs[idx[b]]
			if pa.Fork != pb.Fork {
				return !pa.Fork
			}
			return strings.ToLower(pa.Owner) < strings.ToLower(pb.Owner)
		})
		for n, i := range idx {
			p := prs[i]
			if n == 0 {
				out[i] = Make(p.Repo, p.Number)
			} else {
				out[i] = Make(p.Owner+"-"+p.Repo, p.Number)
			}
		}
	}
	return out
}

func Make(name string, number int) string {
	suffix := "-" + strconv.Itoa(number)
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if max := 63 - len(suffix); len(base) > max {
		base = strings.TrimRight(base[:max], "-")
	}
	if base == "" {
		base = "pr"
	}
	return base + suffix
}
