package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"

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
		logrus.Warn("no netns found")
		os.Exit(0)
	}
	logrus.Debugf("net namespace id <-> name map:\n%+v\n", netNsMap)

	ll, err := netlink.LinkList()
	if err != nil {
		logrus.Fatal(errors.Unwrap(err))
	}
	logrus.Debugf("net link list:\n%+v\n", ll)

	bridgeVethM := make(map[string][]types.Node)
	unBridgedVpairs := []types.Node{}
	bridgeIps := make(map[string]*net.IP)
	loS := []types.Node{}
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
		if link.Attrs().MasterIndex == -1 || veth.MasterIndex == 0 {
			logrus.Debugf("%s not has a bridge as master, MasterIndex: %d", veth.Name, link.Attrs().MasterIndex)
			if veth.PeerName == "" {
				p, err := netlink.LinkByIndex(peerIdx)
				if err != nil {
					logrus.Fatal(err)
				}
				veth.PeerName = p.Attrs().Name
			}
			routes, err := netlink.RouteList(link, 4)
			if err != nil {
				logrus.Fatal(err)
			}
			node := types.Node{
				Type:    types.VethType,
				Veth:    veth.Name,
				Peer:    veth.PeerName,
				PeerId:  peerIdx,
				NetNsID: veth.NetNsID,
			}
			if len(routes) > 0 {
				for _, route := range routes {
					if route.Dst != nil && !route.Dst.IP.IsUnspecified() {
						node.Route = route.Dst.IP
						break
					}
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
		pair := types.Node{
			Type:    types.VethType,
			Veth:    veth.Name,
			PeerId:  peerIdx,
			NetNsID: veth.NetNsID,
		}
		if peerNetNs, ok := netNsMap[veth.NetNsID]; ok {
			peerInNs, err := netutil.GetPeerInNs(peerNetNs, origin, peerIdx)
			if err != nil {
				logrus.Fatal(err)
			}
			pair.NetNsName = peerNetNs
			pair.PeerNameInNetns = peerInNs.Attrs().Name
			pair.Status = peerInNs.Attrs().OperState.String()

			lo, err := netutil.GetLoInNs(peerNetNs, origin)
			if err == nil && lo != nil {
				loS = append(loS, types.Node{
					Type:      types.LoType,
					NetNsName: peerNetNs,
					Status:    lo.Attrs().OperState.String(),
				})
			}
		} else {
			pair.Orphaned = true
		}

		addrs, err := netlink.AddrList(master, syscall.AF_INET)
		if err != nil {
			logrus.Fatal(err)
		}
		if len(addrs) > 0 {
			pair.Master = &types.Bridge{
				Name: bridge,
				IP:   &addrs[0].IP,
			}
			bridgeIps[bridge] = &addrs[0].IP
		}
		bridgeVethM[bridge] = append(v, pair)
	}
	logrus.Debugf("bridgeVethMap: %+v", bridgeVethM)

	switch {
	case *oGraph:
		buf := bytes.Buffer{}
		output, err := formatter.GraphInDOT(bridgeVethM, unBridgedVpairs, loS, bridgeIps, routers)
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
		if link, err := netlink.LinkByIndex(route.LinkIndex); err == nil {
			iface = link.Attrs().Name
		}
		key := route.Gw.String() + "|" + iface
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		routers = append(routers, types.Router{IP: route.Gw, Iface: iface})
	}
	return routers
}

func isDefaultRoute(dst *net.IPNet) bool {
	if dst == nil {
		return true
	}
	ones, bits := dst.Mask.Size()
	return ones == 0 && bits == 32
}
