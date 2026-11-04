package formatter

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vishvananda/netlink"

	"github.com/t1anz0ng/iftree/pkg/types"
)

func Print(w io.Writer, vm map[string][]types.Node, netNsMap map[int]string, vpairs, los []types.Node, routers []types.Router, all bool) {
	var b strings.Builder

	b.WriteString(bannerStyle.Render("⬢ iftree"))
	b.WriteString("  ")
	b.WriteString(subtitleStyle.Render("network topology"))
	b.WriteString("\n")

	loByNs := loopbacksByNamespace(los)
	nsSet := make(map[string]struct{})
	for _, nodes := range vm {
		for _, n := range nodes {
			if n.NetNsName != "" {
				nsSet[n.NetNsName] = struct{}{}
			}
		}
	}

	stats := []string{
		fmt.Sprintf("%d bridges", len(vm)),
		fmt.Sprintf("%d namespaces", len(nsSet)),
	}
	if len(routers) > 0 {
		stats = append(stats, fmt.Sprintf("%d gateways", len(routers)))
	}
	b.WriteString("  ")
	b.WriteString(subtitleStyle.Render(strings.Join(stats, "  ·  ")))
	b.WriteString("\n")

	if len(vm) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionStyle.Render("🌉  Bridges"))
		b.WriteString("\n")
		bridges := sortedBridges(vm)
		for i, bridge := range bridges {
			writeBridge(&b, bridge, vm[bridge], loByNs)
			if i < len(bridges)-1 {
				b.WriteString("\n")
			}
		}
	}

	if len(routers) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionStyle.Render("🌐  Gateways"))
		b.WriteString("\n")
		writeRouters(&b, routers)
	}

	if all && len(vpairs) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionStyle.Render("🔗  Unbridged veth pairs"))
		b.WriteString("\n")
		writeVethPairs(&b, vpairs, netNsMap)
	}

	if len(vm) == 0 && len(routers) == 0 && (!all || len(vpairs) == 0) {
		b.WriteString("  ")
		b.WriteString(subtitleStyle.Render("no network topology found"))
		b.WriteString("\n")
	}

	fmt.Fprint(w, b.String())
}

func writeBridge(b *strings.Builder, name string, nodes []types.Node, loByNs map[string]types.Node) {
	header := []string{"🌉", bridgeStyle.Render(name)}
	if ip := bridgeIP(nodes); ip != "" {
		header = append(header, ipStyle.Render(ip))
	}
	if status := linkStatus(name); status != "" {
		header = append(header, status)
	}
	b.WriteString("  ")
	b.WriteString(strings.Join(header, "  "))
	b.WriteString("\n")

	groups := make(map[string][]types.Node)
	var orphans []types.Node
	for _, n := range nodes {
		if n.NetNsName == "" {
			orphans = append(orphans, n)
			continue
		}
		groups[n.NetNsName] = append(groups[n.NetNsName], n)
	}

	namespaces := make([]string, 0, len(groups))
	for ns := range groups {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	for i, ns := range namespaces {
		lastNs := i == len(namespaces)-1 && len(orphans) == 0
		nsHeader := []string{"📦", netNsStyle.Render(ns), ipStyle.Render(vethCount(len(groups[ns])))}
		fmt.Fprintf(b, "  %s %s\n", treeStyle.Render(treeBranch(lastNs)), strings.Join(nsHeader, "  "))

		prefix := treePrefix(lastNs)
		endpoints := append([]types.Node(nil), groups[ns]...)
		sort.Slice(endpoints, func(i, j int) bool {
			if endpointName(endpoints[i]) == endpointName(endpoints[j]) {
				return endpoints[i].Veth < endpoints[j].Veth
			}
			return endpointName(endpoints[i]) < endpointName(endpoints[j])
		})

		lo, hasLo := loByNs[ns]
		total := len(endpoints)
		if hasLo {
			total++
		}
		for j, n := range endpoints {
			writeEndpoint(b, prefix, j == total-1, n)
		}
		if hasLo {
			fields := []string{"🔁", vethStyle.Render("lo")}
			if status := statusLabel(lo.Status); status != "" {
				fields = append(fields, status)
			}
			fmt.Fprintf(b, "  %s%s %s\n", treeStyle.Render(prefix), treeStyle.Render(treeBranch(true)), strings.Join(fields, "  "))
		}
	}

	for i, n := range orphans {
		last := i == len(orphans)-1
		fields := []string{"⚠️", vethStyle.Render(n.Veth), warnStyle.Render("orphaned")}
		if n.PeerNameInNetns != "" {
			fields = append(fields, peerStyle.Render("⇄ "+n.PeerNameInNetns))
		}
		fmt.Fprintf(b, "  %s %s\n", treeStyle.Render(treeBranch(last)), strings.Join(fields, "  "))
	}

	if len(namespaces) == 0 && len(orphans) == 0 {
		fmt.Fprintf(b, "  %s %s\n", treeStyle.Render(treeBranch(true)), subtitleStyle.Render("no attached interfaces"))
	}
}

func writeEndpoint(b *strings.Builder, prefix string, last bool, n types.Node) {
	fields := []string{"🔌", vethStyle.Render(n.Veth), peerStyle.Render("⇄ " + endpointName(n))}
	if status := statusLabel(n.Status); status != "" {
		fields = append(fields, status)
	}
	fmt.Fprintf(b, "  %s%s %s\n", treeStyle.Render(prefix), treeStyle.Render(treeBranch(last)), strings.Join(fields, "  "))
}

func writeRouters(b *strings.Builder, routers []types.Router) {
	sorted := append([]types.Router(nil), routers...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].IP.Equal(sorted[j].IP) {
			return sorted[i].Iface < sorted[j].Iface
		}
		return sorted[i].IP.String() < sorted[j].IP.String()
	})

	for i, r := range sorted {
		fields := []string{"🌐", ipStyle.Render(r.IP.String())}
		if r.Iface != "" {
			fields = append(fields, peerStyle.Render("via "+r.Iface))
		}
		fmt.Fprintf(b, "  %s %s\n", treeStyle.Render(treeBranch(i == len(sorted)-1)), strings.Join(fields, "  "))
	}
}

func writeVethPairs(b *strings.Builder, vpairs []types.Node, netNsMap map[int]string) {
	seen := make(map[string]struct{})
	pairs := make([]types.Node, 0, len(vpairs))
	for _, v := range vpairs {
		h := hashVethpair(v.Veth, v.Peer)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		pairs = append(pairs, v)
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Veth < pairs[j].Veth
	})

	for i, p := range pairs {
		fields := []string{"🔗", vethStyle.Render(p.Veth), peerStyle.Render("⇄ " + p.Peer)}
		if route := routeText(p); route != "" {
			fields = append(fields, ipStyle.Render(route))
		}
		if ns := netNsMap[p.NetNsID]; ns != "" {
			fields = append(fields, netNsStyle.Render(ns))
		}
		fmt.Fprintf(b, "  %s %s\n", treeStyle.Render(treeBranch(i == len(pairs)-1)), strings.Join(fields, "  "))
	}
}

func loopbacksByNamespace(los []types.Node) map[string]types.Node {
	m := make(map[string]types.Node)
	for _, lo := range los {
		if lo.NetNsName == "" {
			continue
		}
		m[lo.NetNsName] = lo
	}
	return m
}

func sortedBridges(vm map[string][]types.Node) []string {
	names := make([]string, 0, len(vm))
	for name := range vm {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func bridgeIP(nodes []types.Node) string {
	for _, n := range nodes {
		if n.Master != nil && n.Master.IP != nil {
			return n.Master.IP.String()
		}
	}
	return ""
}

func endpointName(n types.Node) string {
	if n.PeerNameInNetns != "" {
		return n.PeerNameInNetns
	}
	return "(unknown)"
}

func routeText(n types.Node) string {
	if len(n.Route) == 0 || n.Route.IsUnspecified() {
		return ""
	}
	return "route " + n.Route.String()
}

func vethCount(n int) string {
	if n == 1 {
		return "1 veth"
	}
	return fmt.Sprintf("%d veths", n)
}

func linkStatus(name string) string {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return ""
	}
	return statusLabel(link.Attrs().OperState.String())
}

func statusLabel(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "":
		return ""
	case "up":
		return upStyle.Render("● up")
	case "down", "lowerlayerdown", "notpresent":
		return downStyle.Render("○ " + s)
	default:
		return warnStyle.Render("○ " + s)
	}
}

func treeBranch(last bool) string {
	if last {
		return "└─"
	}
	return "├─"
}

func treePrefix(last bool) string {
	if last {
		return "   "
	}
	return "│  "
}
