package types

import (
	"net"
	"strings"
)

type NodeType int

const (
	VethType NodeType = iota
	BridgeType
	LoType
)

type Node struct {
	ID   string
	Type NodeType

	Veth            string
	Peer            string
	PeerNameInNetns string
	PeerId          int
	Orphaned        bool
	NetNsID         int
	NetNsName       string
	Master          *Bridge
	Route           net.IP
	Addrs           []net.IPNet
	PeerAddrs       []net.IPNet

	Name   string
	Status string
}

func (n *Node) Label() string {
	if n.Name == "" {
		n.Name = n.Veth
	}
	switch n.Type {
	case LoType:
		return "lo"
	default:
		return strings.TrimSpace(n.Name)
	}
}

type Bridge struct {
	Name  string
	Addrs []net.IPNet
}

type Router struct {
	IP    net.IP
	Iface string
	Addrs []net.IPNet
}
