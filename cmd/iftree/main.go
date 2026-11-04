package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/containerd/nerdctl/pkg/rootlessutil"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"github.com/t1anz0ng/iftree/pkg/formatter"
	"github.com/t1anz0ng/iftree/pkg/netutil"
	"github.com/t1anz0ng/iftree/pkg/types"
)

var (
	debug = pflag.BoolP("debug", "d", false, "print debug message")

	oNotBridgedVeths = pflag.BoolP("all", "a", true, "show all veths, including not bridged.")
	oGraph           = pflag.BoolP("graph", "g", false, "output a topology graph")
	oGraphType       = pflag.StringP("gtype", "T", "dot", `graph output type, "jpg", "png", "svg", "dot"(graphviz dot language(https://graphviz.org/doc/info/lang.html)`)
	oGraphName       = pflag.StringP("output", "O", "output", "graph output name/path")

	oTable = pflag.BoolP("table", "t", false, "output in table")

	help = pflag.BoolP("help", "h", false, "")

	version = "unknown"

	defaultOutput = os.Stdout
)

func init() {
	pflag.Usage = func() {
		fmt.Printf(`Usage:
  iftree [options]

Examples:
  generate the terminal tree
    # sudo iftree
  generate a png graph named "output.png"
    # sudo iftree --graph -Tpng -Ooutput.png
  generate a table
    # sudo iftree --table

Options:
  -d, --debug              print debug messages
  -t, --table              output in table
  -g, --graph              output a topology graph
  -O, --output string      graph output name/path (default "output")
  -T, --gtype string       graph output type: jpg, png, svg, dot (default "dot")
  -a, --all                show unbridged veth pairs too (default true)
  -h, --help               show this help message

version: %s
`, version)
	}
}

func helper() error {
	if *oGraph && *oTable {
		return fmt.Errorf(`only one of "graph", or "table" can be set`)
	}
	if *help {
		pflag.Usage()
		os.Exit(0)
	}
	return nil
}

func main() {
	pflag.Parse()
	if err := helper(); err != nil {
		logrus.Fatal(err)
	}
	if rootlessutil.IsRootless() {
		logrus.Error("iftree must be run as root to enter ns")
		os.Exit(1)
	}
	logrus.SetLevel(logrus.InfoLevel)
	if *debug {
		logrus.SetReportCaller(true)
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.ErrorLevel)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	netNsMap, err := netutil.NetNsMap()
	if err != nil {
		logrus.Fatal(err)
	}
	if len(netNsMap) == 0 {
		logrus.Debug("no netns found")
	}
	logrus.Debugf("net namespace id <-> name map:\n%+v\n", netNsMap)

	ll, err := netlink.LinkList()
	if err != nil {
		logrus.Fatal(errors.Unwrap(err))
	}
	logrus.Debugf("net link list:\n%+v\n", ll)

	bridgeVethM := make(map[string][]types.Node)
	unBridgedVpairs := []types.Node{}
	bridgeAddrs := make(map[string][]net.IPNet)
	loS := []types.Node{}
	loSeen := make(map[string]struct{})
	routers := defaultRouters()
	logrus.Debugf("default routers: %+v", routers)

	origin, err := netns.Get()
	if err != nil {
		logrus.Fatalf("failed get current netns, %v", err)
	}

	for _, link := range ll {
		veth, ok := link.(*netlink.Veth)
		if !ok {
			logrus.Debugf("skip %s, type: %s", link.Attrs().Name, link.Type())
			continue
		}
		logrus.Debugf("veth device: %+v", veth)

		peerIdx, err := netlink.VethPeerIndex(veth)
		if err != nil {
			logrus.Fatal(err)
		}
		vethAddrs := linkAddrs(link)
		if link.Attrs().MasterIndex == -1 || veth.MasterIndex == 0 {
			logrus.Debugf("%s not has a bridge as master, MasterIndex: %d", veth.Name, link.Attrs().MasterIndex)
			node := types.Node{
				Type:    types.VethType,
				Veth:    veth.Name,
				Peer:    veth.PeerName,
				PeerId:  peerIdx,
				NetNsID: veth.NetNsID,
				Addrs:   vethAddrs,
			}
			if peerNetNs, ok := netNsMap[veth.NetNsID]; ok {
				peerInNs, peerAddrs, err := netutil.GetPeerInNs(peerNetNs, origin, peerIdx)
				if err != nil {
					logrus.Debugf("unable to inspect peer of %s in %s: %v", veth.Name, peerNetNs, err)
				} else if peerInNs != nil {
					node.NetNsName = peerNetNs
					node.PeerNameInNetns = peerInNs.Attrs().Name
					node.PeerAddrs = addrNets(peerAddrs)
					node.Status = peerInNs.Attrs().OperState.String()
					if node.Peer == "" {
						node.Peer = node.PeerNameInNetns
					}
				}
			}
			if node.Peer == "" {
				p, err := netlink.LinkByIndex(peerIdx)
				if err != nil {
					logrus.Debugf("unable to resolve peer of %s: %v", veth.Name, err)
				} else {
					node.Peer = p.Attrs().Name
				}
			}
			routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
			if err != nil {
				logrus.Fatal(err)
			}
			for _, route := range routes {
				if route.Dst != nil && !route.Dst.IP.IsUnspecified() {
					node.Route = route.Dst.IP
					break
				}
			}
			unBridgedVpairs = append(unBridgedVpairs, node)
			continue
		}

		master, err := netlink.LinkByIndex(veth.Attrs().MasterIndex)
		if err != nil {
			logrus.Fatal(err)
		}

		if _, ok := master.(*netlink.Bridge); !ok {
			continue
		}
		bridge := master.Attrs().Name
		v, ok := bridgeVethM[bridge]
		if !ok {
			bridgeVethM[bridge] = []types.Node{}
		}
		masterAddrs, ok := bridgeAddrs[bridge]
		if !ok {
			masterAddrs = linkAddrs(master)
			bridgeAddrs[bridge] = masterAddrs
		}
		pair := types.Node{
			Type:    types.VethType,
			Veth:    veth.Name,
			PeerId:  peerIdx,
			NetNsID: veth.NetNsID,
			Addrs:   vethAddrs,
			Master:  &types.Bridge{Name: bridge, Addrs: masterAddrs},
		}
		if peerNetNs, ok := netNsMap[veth.NetNsID]; ok {
			peerInNs, peerAddrs, err := netutil.GetPeerInNs(peerNetNs, origin, peerIdx)
			if err != nil {
				logrus.Fatal(err)
			}
			pair.NetNsName = peerNetNs
			pair.PeerNameInNetns = peerInNs.Attrs().Name
			pair.PeerAddrs = addrNets(peerAddrs)
			pair.Status = peerInNs.Attrs().OperState.String()

			if _, ok := loSeen[peerNetNs]; !ok {
				lo, loAddrs, err := netutil.GetLoInNs(peerNetNs, origin)
				if err == nil && lo != nil {
					loS = append(loS, types.Node{
						Type:      types.LoType,
						NetNsName: peerNetNs,
						Status:    lo.Attrs().OperState.String(),
						Addrs:     addrNets(loAddrs),
					})
					loSeen[peerNetNs] = struct{}{}
				}
			}
		} else {
			pair.Orphaned = true
		}

		bridgeVethM[bridge] = append(v, pair)
	}
	logrus.Debugf("bridgeVethMap: %+v", bridgeVethM)

	switch {
	case *oGraph:
		buf := bytes.Buffer{}
		output, err := formatter.GraphInDOT(bridgeVethM, unBridgedVpairs, loS, bridgeAddrs, routers)
		if err != nil {
			logrus.Fatal(errors.Unwrap(err))
		}
		fmt.Fprintln(&buf, output)
		gType := strings.ToLower(*oGraphType)

		switch gType {
		case "dot":
			_, err = io.Copy(defaultOutput, &buf)
		case "jpg", "png", "svg":
			if !pflag.CommandLine.Changed("output") && !pflag.CommandLine.Changed("gtype") {
				logrus.Warn(`default output dst file: "output.png"`)
			}
			if fn, err := formatter.GenImage(buf.Bytes(), oGraphName, gType); err != nil {
				os.Remove(fn)
				logrus.Fatal(errors.Unwrap(err))
			}
		default:
			logrus.Error("unknown image type")
			os.Exit(1)
		}
		if err != nil {
			logrus.Fatal(err)
		}
		return
	case *oTable:
		if len(bridgeVethM) > 0 {
			err := formatter.Table(defaultOutput, bridgeVethM)
			if err != nil {
				logrus.Fatal(errors.Unwrap(err))
			}
		}
		if *oNotBridgedVeths && len(unBridgedVpairs) > 0 {
			formatter.TableParis(defaultOutput, unBridgedVpairs)
		}
		return
	default:
		formatter.Print(defaultOutput, bridgeVethM, netNsMap, unBridgedVpairs, loS, routers, *oNotBridgedVeths)
	}
}

func defaultRouters() []types.Router {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		logrus.Debugf("unable to list routes: %v", err)
		return nil
	}

	seen := make(map[string]struct{})
	routers := make([]types.Router, 0)
	for _, route := range routes {
		if route.Gw == nil || !isDefaultRoute(route.Dst) {
			continue
		}
		iface := ""
		var addrs []net.IPNet
		if link, err := netlink.LinkByIndex(route.LinkIndex); err == nil {
			iface = link.Attrs().Name
			addrs = linkAddrs(link)
		}
		key := route.Gw.String() + "|" + iface
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		routers = append(routers, types.Router{IP: route.Gw, Iface: iface, Addrs: addrs})
	}
	return routers
}

func linkAddrs(link netlink.Link) []net.IPNet {
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		logrus.Debugf("unable to list addresses for %s: %v", link.Attrs().Name, err)
		return nil
	}
	return addrNets(addrs)
}

func addrNets(addrs []netlink.Addr) []net.IPNet {
	nets := make([]net.IPNet, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		if addr.IPNet == nil || len(addr.IPNet.IP) == 0 {
			continue
		}
		value := *addr.IPNet
		key := value.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		nets = append(nets, value)
	}
	sort.Slice(nets, func(i, j int) bool {
		return nets[i].String() < nets[j].String()
	})
	return nets
}

func isDefaultRoute(dst *net.IPNet) bool {
	if dst == nil {
		return true
	}
	ones, bits := dst.Mask.Size()
	return ones == 0 && bits == 32
}
