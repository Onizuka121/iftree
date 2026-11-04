package formatter

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

func addressText(addrs []net.IPNet) string {
	values := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		if len(addr.IP) == 0 {
			continue
		}
		value := addr.IP.String()
		if ones, bits := addr.Mask.Size(); bits > 0 {
			value = fmt.Sprintf("%s/%d", value, ones)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
