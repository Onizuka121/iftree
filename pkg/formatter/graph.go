package formatter

import (
	"fmt"
	"html"
	"net"
	"os"
	"sort"
	"strings"

	graphviz "github.com/goccy/go-graphviz"
	"github.com/pkg/errors"

	"github.com/t1anz0ng/iftree/pkg/types"
)

const (
	inkColor        = "#0f172a"
	mutedInkColor   = "#64748b"
	bridgeInkColor  = "#065f46"
	bridgeFillColor = "#ecfdf5"
	bridgeEdgeColor = "#6ee7b7"

	nsColor     = "#8b5cf6"
	nsFillColor = "#faf5ff"
	nsEdgeColor = "#d8b4fe"
	nsNodeFill  = "#f3e8ff"
	nsNodeColor = "#c084fc"
	nsNodeInk   = "#4c1d95"

	routerInkColor  = "#075985"
	routerFillColor = "#e0f2fe"
	routerEdgeColor = "#7dd3fc"

	loInkColor  = "#854d0e"
	loFillColor = "#fefce8"
	loEdgeColor = "#fde047"

	hostFillColor   = "#ffffff"
	hostEdgeColor   = "#cbd5e1"
	linkColor       = "#94a3b8"
	pairColor       = "#a855f7"
	gatewayEdge     = "#38bdf8"
	unbridgedColor  = "#b45309"
	unbridgedFill   = "#fffbeb"
	unbridgedBorder = "#fcd34d"
	unbridgedEdge   = "#f59e0b"
)

type graphEdge struct {
	from  string
	to    string
	attrs string
}

type idSequence struct {
	n int
}

func (s *idSequence) next(prefix string) string {
	s.n++
	return fmt.Sprintf("%s_%d", prefix, s.n)
}

type hostVeth struct {
	node   types.Node
	id     string
	bridge string
	peerID string
}

type routerUplink struct {
	name  string
	id    string
	addrs []net.IPNet
}

type nsEndpoint struct {
	node   types.Node
	hostID string
	peerID string
}

type nsCluster struct {
	name      string
	endpoints []nsEndpoint
	loopback  *types.Node
}

type nodeStyle struct {
	shape    string
	fill     string
	border   string
	penwidth string
}

type nodeLine struct {
	text  string
	color string
}

func GraphInDOT(brVethsM map[string][]types.Node, vpairs, los []types.Node, bridgeAddrs map[string][]net.IPNet, routers []types.Router) (string, error) {
	ids := &idSequence{}
	var b strings.Builder

	bridgeNames := make([]string, 0, len(brVethsM))
	for bridge := range brVethsM {
		bridgeNames = append(bridgeNames, bridge)
	}
	sort.Strings(bridgeNames)

	bridgeIDs := make(map[string]string, len(bridgeNames))
	for _, bridge := range bridgeNames {
		bridgeIDs[bridge] = ids.next("bridge")
	}

	hostVeths := make([]hostVeth, 0)
	clustersByName := make(map[string]*nsCluster)
	for _, bridge := range bridgeNames {
		nodes := append([]types.Node(nil), brVethsM[bridge]...)
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Veth == nodes[j].Veth {
				return nodes[i].NetNsName < nodes[j].NetNsName
			}
			return nodes[i].Veth < nodes[j].Veth
		})
		for _, vp := range nodes {
			hv := hostVeth{
				node:   vp,
				id:     ids.next("veth"),
				bridge: bridge,
				peerID: ids.next("peer"),
			}
			hostVeths = append(hostVeths, hv)
			if vp.NetNsName == "" {
				continue
			}
			cluster := clustersByName[vp.NetNsName]
			if cluster == nil {
				cluster = &nsCluster{name: vp.NetNsName}
				clustersByName[vp.NetNsName] = cluster
			}
			cluster.endpoints = append(cluster.endpoints, nsEndpoint{
				node:   vp,
				hostID: hv.id,
				peerID: hv.peerID,
			})
		}
	}

	for _, lo := range los {
		if lo.NetNsName == "" || lo.Status == "" {
			continue
		}
		if cluster := clustersByName[lo.NetNsName]; cluster != nil {
			loopback := lo
			cluster.loopback = &loopback
		}
	}

	clusters := make([]*nsCluster, 0, len(clustersByName))
	for _, cluster := range clustersByName {
		sort.Slice(cluster.endpoints, func(i, j int) bool {
			if cluster.endpoints[i].node.Veth == cluster.endpoints[j].node.Veth {
				return cluster.endpoints[i].node.PeerNameInNetns < cluster.endpoints[j].node.PeerNameInNetns
			}
			return cluster.endpoints[i].node.Veth < cluster.endpoints[j].node.Veth
		})
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].name < clusters[j].name
	})

	edges := make([]graphEdge, 0)
	uplinks := makeRouterUplinks(routers, bridgeIDs, ids)

	b.WriteString("digraph iftree {\n")
	writeGraphHeader(&b, len(bridgeNames), len(clusters), len(routers))

	routerIDs := writeRouterNodes(&b, ids, routers, bridgeIDs, uplinks, &edges)
	if len(routerIDs) > 0 {
		fmt.Fprintf(&b, "  { rank=source; %s; }\n", strings.Join(routerIDs, "; "))
	}

	if len(bridgeNames) > 0 || len(uplinks) > 0 {
		writeHostCluster(&b, bridgeNames, bridgeIDs, bridgeAddrs, hostVeths, uplinks, &edges)
	}

	for _, cluster := range clusters {
		writeNamespaceCluster(&b, ids, cluster, &edges)
	}

	writeUnbridgedCluster(&b, ids, vpairs, &edges)

	for _, edge := range edges {
		fmt.Fprintf(&b, "  %s -> %s [%s];\n", edge.from, edge.to, edge.attrs)
	}

	b.WriteString("}\n")
	return b.String(), nil
}

func writeGraphHeader(b *strings.Builder, bridges, namespaces, routers int) {
	stats := fmt.Sprintf("%s · %s", countText(bridges, "bridge"), countText(namespaces, "namespace"))
	if routers > 0 {
		stats += " · " + countText(routers, "gateway")
	}

	b.WriteString("  graph [\n")
	b.WriteString("    layout=\"dot\",\n")
	b.WriteString("    rankdir=\"TB\",\n")
	b.WriteString("    bgcolor=\"#ffffff\",\n")
	b.WriteString("    pad=\"0.5\",\n")
	b.WriteString("    nodesep=\"0.45\",\n")
	b.WriteString("    ranksep=\"0.8\",\n")
	b.WriteString("    splines=\"spline\",\n")
	b.WriteString("    overlap=\"false\",\n")
	b.WriteString("    compound=\"true\",\n")
	b.WriteString("    newrank=\"true\",\n")
	b.WriteString("    dpi=\"150\",\n")
	b.WriteString("    fontname=\"DejaVu Sans\",\n")
	b.WriteString("    fontsize=\"10\",\n")
	b.WriteString("    labelloc=\"t\",\n")
	b.WriteString("    labeljust=\"c\",\n")
	fmt.Fprintf(b, "    label=<<TABLE BORDER=\"0\" CELLBORDER=\"0\" CELLSPACING=\"0\" CELLPADDING=\"0\"><TR><TD><FONT FACE=\"DejaVu Sans\" POINT-SIZE=\"23\" COLOR=\"%s\"><B>iftree</B></FONT></TD></TR><TR><TD><FONT FACE=\"DejaVu Sans\" POINT-SIZE=\"10\" COLOR=\"%s\">%s</FONT></TD></TR></TABLE>>\n",
		bridgeInkColor, mutedInkColor, html.EscapeString(stats))
	b.WriteString("  ];\n")
	b.WriteString("  node [\n")
	b.WriteString("    shape=\"box\",\n")
	b.WriteString("    style=\"rounded,filled\",\n")
	b.WriteString("    fontname=\"DejaVu Sans\",\n")
	b.WriteString("    fontsize=\"11\",\n")
	b.WriteString("    margin=\"0.16,0.10\",\n")
	b.WriteString("    penwidth=\"1.2\",\n")
	b.WriteString("    color=\"#cbd5e1\",\n")
	b.WriteString("    fillcolor=\"#ffffff\"\n")
	b.WriteString("  ];\n")
	b.WriteString("  edge [\n")
	b.WriteString("    color=\"#94a3b8\",\n")
	b.WriteString("    penwidth=\"1.3\",\n")
	b.WriteString("    arrowsize=\"0.7\",\n")
	b.WriteString("    dir=\"none\",\n")
	b.WriteString("    fontname=\"DejaVu Sans\",\n")
	b.WriteString("    fontsize=\"9\",\n")
	b.WriteString("    fontcolor=\"#64748b\"\n")
	b.WriteString("  ];\n")
}

func writeHostCluster(b *strings.Builder, bridges []string, bridgeIDs map[string]string, bridgeAddrs map[string][]net.IPNet, veths []hostVeth, uplinks []routerUplink, edges *[]graphEdge) {
	b.WriteString("  subgraph cluster_host {\n")
	b.WriteString(`    label=<<FONT FACE="DejaVu Sans" POINT-SIZE="11" COLOR="#64748b"><B>⌂  host</B></FONT>>;` + "\n")
	b.WriteString("    labeljust=\"l\";\n")
	b.WriteString("    labelloc=\"t\";\n")
	b.WriteString("    style=\"rounded,filled\";\n")
	b.WriteString("    fillcolor=\"#f8fafc\";\n")
	b.WriteString("    color=\"#e2e8f0\";\n")
	b.WriteString("    penwidth=\"1.2\";\n")
	b.WriteString("    margin=\"18\";\n")

	for _, bridge := range bridges {
		lines := addressLines(bridgeAddrs[bridge])
		if len(lines) == 0 {
			lines = []nodeLine{{text: "layer 2 bridge", color: mutedInkColor}}
		}
		label := nodeLabelRows(bridge, bridgeInkColor, 12, lines...)
		writeNode(b, bridgeIDs[bridge], label, nodeStyle{
			shape:    "hexagon",
			fill:     bridgeFillColor,
			border:   bridgeEdgeColor,
			penwidth: "1.6",
		})
	}

	for _, uplink := range uplinks {
		lines := addressLines(uplink.addrs)
		lines = append(lines, nodeLine{text: "uplink", color: mutedInkColor})
		label := nodeLabelRows(uplink.name, routerInkColor, 10, lines...)
		writeNode(b, uplink.id, label, nodeStyle{
			shape:    "component",
			fill:     routerFillColor,
			border:   routerEdgeColor,
			penwidth: "1.1",
		})
	}

	for _, hv := range veths {
		lines := addressLines(hv.node.Addrs)
		if hv.node.NetNsName == "" {
			lines = append(lines, nodeLine{text: "orphaned", color: unbridgedColor})
		}
		label := nodeLabelRows(hv.node.Veth, inkColor, 10, lines...)
		writeNode(b, hv.id, label, nodeStyle{
			shape:    "component",
			fill:     hostFillColor,
			border:   hostEdgeColor,
			penwidth: "1.1",
		})
		*edges = append(*edges, graphEdge{
			from:  bridgeIDs[hv.bridge],
			to:    hv.id,
			attrs: fmt.Sprintf(`color="%s", penwidth="1.3"`, linkColor),
		})
	}

	b.WriteString("  }\n")
}

func writeNamespaceCluster(b *strings.Builder, ids *idSequence, cluster *nsCluster, edges *[]graphEdge) {
	fmt.Fprintf(b, "  subgraph cluster_%s {\n", ids.next("ns"))
	fmt.Fprintf(b, "    label=%s;\n", clusterLabel("NETNS", cluster.name, nsColor))
	b.WriteString("    labeljust=\"l\";\n")
	b.WriteString("    labelloc=\"b\";\n")
	b.WriteString("    style=\"rounded,filled\";\n")
	fmt.Fprintf(b, "    fillcolor=\"%s\";\n", nsFillColor)
	fmt.Fprintf(b, "    color=\"%s\";\n", nsEdgeColor)
	b.WriteString("    penwidth=\"1.2\";\n")
	b.WriteString("    margin=\"16\";\n")

	sameRank := make([]string, 0, len(cluster.endpoints)+1)
	for _, ep := range cluster.endpoints {
		lines := addressLines(ep.node.PeerAddrs)
		if text, color := statusSubtitle(ep.node.Status); text != "" {
			lines = append(lines, nodeLine{text: text, color: color})
		}
		label := nodeLabelRows(endpointName(ep.node), nsNodeInk, 10, lines...)
		writeNode(b, ep.peerID, label, nodeStyle{
			shape:    "component",
			fill:     nsNodeFill,
			border:   nsNodeColor,
			penwidth: "1.1",
		})
		sameRank = append(sameRank, ep.peerID)
		*edges = append(*edges, graphEdge{
			from:  ep.hostID,
			to:    ep.peerID,
			attrs: fmt.Sprintf(`color="%s", penwidth="1.6"`, pairColor),
		})
	}

	if cluster.loopback != nil {
		lines := addressLines(cluster.loopback.Addrs)
		if text, color := statusSubtitle(cluster.loopback.Status); text != "" {
			lines = append(lines, nodeLine{text: text, color: color})
		}
		loID := ids.next("lo")
		label := nodeLabelRows("lo", loInkColor, 10, lines...)
		writeNode(b, loID, label, nodeStyle{
			shape:    "ellipse",
			fill:     loFillColor,
			border:   loEdgeColor,
			penwidth: "1.1",
		})
		sameRank = append(sameRank, loID)
	}

	if len(sameRank) > 1 {
		fmt.Fprintf(b, "    { rank=same; %s; }\n", strings.Join(sameRank, "; "))
	}
	b.WriteString("  }\n")
}

func writeRouterNodes(b *strings.Builder, ids *idSequence, routers []types.Router, bridgeIDs map[string]string, uplinks []routerUplink, edges *[]graphEdge) []string {
	uplinkIDs := make(map[string]string, len(uplinks))
	for _, uplink := range uplinks {
		uplinkIDs[uplink.name] = uplink.id
	}

	seen := make(map[string]struct{})
	routerIDs := make([]string, 0, len(routers))
	ordered := append([]types.Router(nil), routers...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].IP.Equal(ordered[j].IP) {
			return ordered[i].Iface < ordered[j].Iface
		}
		return ordered[i].IP.String() < ordered[j].IP.String()
	})

	for _, router := range ordered {
		if len(router.IP) == 0 {
			continue
		}
		key := router.IP.String() + "|" + router.Iface
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		subtitle := router.IP.String()
		if router.Iface != "" {
			subtitle += "  ·  via " + router.Iface
		}
		id := ids.next("router")
		label := nodeLabel("gateway", routerInkColor, subtitle, mutedInkColor, 12)
		writeNode(b, id, label, nodeStyle{
			shape:    "box3d",
			fill:     routerFillColor,
			border:   routerEdgeColor,
			penwidth: "1.4",
		})
		routerIDs = append(routerIDs, id)

		if bridgeID, ok := bridgeIDs[router.Iface]; ok {
			*edges = append(*edges, graphEdge{
				from:  id,
				to:    bridgeID,
				attrs: fmt.Sprintf(`color="%s", style="dashed", penwidth="1.4", dir="forward"`, gatewayEdge),
			})
		} else if uplinkID, ok := uplinkIDs[router.Iface]; ok {
			*edges = append(*edges, graphEdge{
				from:  id,
				to:    uplinkID,
				attrs: fmt.Sprintf(`color="%s", style="dashed", penwidth="1.4", dir="forward"`, gatewayEdge),
			})
		}
	}

	return routerIDs
}

func makeRouterUplinks(routers []types.Router, bridgeIDs map[string]string, ids *idSequence) []routerUplink {
	seen := make(map[string]struct{})
	addrs := make(map[string][]net.IPNet)
	names := make([]string, 0)
	for _, router := range routers {
		if router.Iface == "" {
			continue
		}
		if _, ok := bridgeIDs[router.Iface]; ok {
			continue
		}
		if _, ok := seen[router.Iface]; ok {
			continue
		}
		seen[router.Iface] = struct{}{}
		names = append(names, router.Iface)
		if len(router.Addrs) > 0 {
			addrs[router.Iface] = router.Addrs
		}
	}
	sort.Strings(names)

	uplinks := make([]routerUplink, 0, len(names))
	for _, name := range names {
		uplinks = append(uplinks, routerUplink{name: name, id: ids.next("uplink"), addrs: addrs[name]})
	}
	return uplinks
}

func writeUnbridgedCluster(b *strings.Builder, ids *idSequence, vpairs []types.Node, edges *[]graphEdge) {
	seen := make(map[string]struct{})
	pairs := make([]types.Node, 0, len(vpairs))
	for _, vp := range vpairs {
		key := hashVethpair(vp.Veth, vp.Peer)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pairs = append(pairs, vp)
	}
	if len(pairs) == 0 {
		return
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Veth == pairs[j].Veth {
			return pairs[i].Peer < pairs[j].Peer
		}
		return pairs[i].Veth < pairs[j].Veth
	})

	b.WriteString("  subgraph cluster_unbridged {\n")
	fmt.Fprintf(b, "    label=%s;\n", clusterLabel("NOT BRIDGED", "", unbridgedColor))
	b.WriteString("    labeljust=\"l\";\n")
	b.WriteString("    labelloc=\"t\";\n")
	b.WriteString("    style=\"rounded,filled\";\n")
	fmt.Fprintf(b, "    fillcolor=\"%s\";\n", unbridgedFill)
	fmt.Fprintf(b, "    color=\"%s\";\n", unbridgedBorder)
	b.WriteString("    penwidth=\"1.2\";\n")
	b.WriteString("    margin=\"16\";\n")

	for _, pair := range pairs {
		leftLines := addressLines(pair.Addrs)
		if route := routeText(pair); route != "" {
			leftLines = append(leftLines, nodeLine{text: route, color: mutedInkColor})
		}
		leftID := ids.next("unbridged")
		writeNode(b, leftID, unbridgedCard(pair.Veth, leftLines...), nodeStyle{
			shape:    "component",
			fill:     unbridgedFill,
			border:   unbridgedBorder,
			penwidth: "1.1",
		})

		if pair.Peer == "" && pair.PeerNameInNetns == "" {
			continue
		}
		rightID := ids.next("unbridged")
		rightLines := addressLines(pair.PeerAddrs)
		writeNode(b, rightID, unbridgedCard(unbridgedPeerName(pair), rightLines...), nodeStyle{
			shape:    "component",
			fill:     unbridgedFill,
			border:   unbridgedBorder,
			penwidth: "1.1",
		})
		*edges = append(*edges, graphEdge{
			from:  leftID,
			to:    rightID,
			attrs: fmt.Sprintf(`color="%s", penwidth="1.4"`, unbridgedEdge),
		})
	}

	b.WriteString("  }\n")
}

func unbridgedCard(name string, lines ...nodeLine) string {
	return nodeLabelRows(name, unbridgedColor, 10, lines...)
}

func unbridgedPeerName(node types.Node) string {
	if node.PeerNameInNetns != "" {
		return node.PeerNameInNetns
	}
	return node.Peer
}

func addressLines(addrs []net.IPNet) []nodeLine {
	if text := addressText(addrs); text != "" {
		return []nodeLine{{text: text, color: mutedInkColor}}
	}
	return nil
}

func nodeLabel(title, titleColor, subtitle, subColor string, titleSize int) string {
	return nodeLabelRows(title, titleColor, titleSize, nodeLine{text: subtitle, color: subColor})
}

func nodeLabelRows(title, titleColor string, titleSize int, lines ...nodeLine) string {
	if titleSize == 0 {
		titleSize = 11
	}

	var b strings.Builder
	b.WriteString(`<<TABLE BORDER="0" CELLBORDER="0" CELLSPACING="0" CELLPADDING="1">`)
	fmt.Fprintf(&b, `<TR><TD ALIGN="CENTER"><FONT FACE="DejaVu Sans" POINT-SIZE="%d" COLOR="%s"><B>%s</B></FONT></TD></TR>`, titleSize, titleColor, html.EscapeString(title))
	for _, line := range lines {
		if line.text == "" {
			continue
		}
		fmt.Fprintf(&b, `<TR><TD ALIGN="CENTER"><FONT FACE="DejaVu Sans" POINT-SIZE="9" COLOR="%s">%s</FONT></TD></TR>`, line.color, html.EscapeString(line.text))
	}
	b.WriteString(`</TABLE>>`)
	return b.String()
}

func clusterLabel(kind, text, color string) string {
	value := fmt.Sprintf(`<FONT FACE="DejaVu Sans" POINT-SIZE="9" COLOR="%s"><B>%s</B></FONT>`, color, html.EscapeString(kind))
	if text != "" {
		value += fmt.Sprintf(`  <FONT FACE="DejaVu Sans" POINT-SIZE="11" COLOR="#334155">%s</FONT>`, html.EscapeString(text))
	}
	return fmt.Sprintf(`<<TABLE BORDER="0" CELLBORDER="0" CELLSPACING="0" CELLPADDING="0"><TR><TD>%s</TD></TR></TABLE>>`, value)
}

func statusSubtitle(status string) (string, string) {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "":
		return "", mutedInkColor
	case "up":
		return "● up", "#16a34a"
	case "down", "lowerlayerdown", "notpresent":
		return "○ " + s, "#dc2626"
	default:
		return "○ " + s, unbridgedColor
	}
}

func writeNode(b *strings.Builder, id, label string, style nodeStyle) {
	fmt.Fprintf(b, "    %s [label=%s, shape=\"%s\", fillcolor=\"%s\", color=\"%s\", penwidth=\"%s\"];\n",
		id, label, style.shape, style.fill, style.border, style.penwidth)
}

func GenImage(data []byte, oGraphName *string, gType string) (fn string, err error) {
	g := graphviz.New()
	defer g.Close()

	graph, err := graphviz.ParseBytes(data)
	if err != nil {
		return "", errors.Wrap(err, "parse dot bytes")
	}
	defer graph.Close()
	fn = fmt.Sprintf("%s.%s", *oGraphName, gType)
	f, err := os.Create(fn)
	if err != nil {
		return fn, errors.Wrapf(err, "create file `%s`", fn)
	}
	defer f.Close()

	switch gType {
	case "jpg":
		err = g.Render(graph, graphviz.JPG, f)
	case "png":
		err = g.Render(graph, graphviz.PNG, f)
	case "svg":
		err = g.Render(graph, graphviz.SVG, f)
	default:
		return fn, fmt.Errorf("unknown graph type %s", gType)
	}
	if err != nil {
		return fn, errors.Wrap(err, "render image")
	}
	return fn, nil
}
