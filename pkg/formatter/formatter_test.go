package formatter

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/t1anz0ng/iftree/pkg/types"
)

func testNet(t *testing.T, cidr string) net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return *n
}

func TestAddressText(t *testing.T) {
	addrs := []net.IPNet{
		testNet(t, "::1/128"),
		testNet(t, "127.0.0.1/8"),
		testNet(t, "127.0.0.1/8"),
	}
	if got, want := addressText(addrs), "127.0.0.1/8, ::1/128"; got != want {
		t.Fatalf("addressText() = %q, want %q", got, want)
	}
}

func TestGraphInDOTIncludesAddresses(t *testing.T) {
	masterAddrs := []net.IPNet{testNet(t, "172.17.0.1/16")}
	master := &types.Bridge{Name: "docker0", Addrs: masterAddrs}
	node := types.Node{
		Type:            types.VethType,
		Veth:            "veth0",
		PeerNameInNetns: "eth0",
		NetNsName:       "ns",
		Status:          "up",
		PeerAddrs:       []net.IPNet{testNet(t, "172.17.0.2/16")},
		Master:          master,
	}

	dot, err := GraphInDOT(
		map[string][]types.Node{"docker0": {node}},
		nil,
		nil,
		map[string][]net.IPNet{"docker0": masterAddrs},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docker0", "172.17.0.1/16", "172.17.0.2/16"} {
		if !strings.Contains(dot, want) {
			t.Fatalf("GraphInDOT() does not contain %q", want)
		}
	}
}

func TestTableIncludesOrphanedVeth(t *testing.T) {
	vm := map[string][]types.Node{
		"docker0": {{
			Type:   types.VethType,
			Veth:   "veth0",
			Peer:   "veth1",
			Master: &types.Bridge{Name: "docker0"},
		}},
	}

	var out bytes.Buffer
	if err := Table(&out, vm); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"veth0", "orphaned"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("Table() does not contain %q", want)
		}
	}
}

func TestVethPairKeyDoesNotCollide(t *testing.T) {
	if vethPairKey("ab", "cd") == vethPairKey("ac", "bd") {
		t.Fatal("vethPairKey() collides for distinct pairs")
	}
}

func TestUnbridgedMasterIsRendered(t *testing.T) {
	node := types.Node{
		Type:       types.VethType,
		Veth:       "veth0",
		Peer:       "veth1",
		MasterName: "ovs0",
	}

	var out bytes.Buffer
	Print(&out, nil, nil, []types.Node{node}, nil, nil, true)
	if !strings.Contains(out.String(), "master ovs0") {
		t.Fatal("Print() does not show the non-bridge master")
	}

	dot, err := GraphInDOT(nil, []types.Node{node}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dot, "master ovs0") {
		t.Fatal("GraphInDOT() does not show the non-bridge master")
	}
}

func TestPrintIncludesAddresses(t *testing.T) {
	master := &types.Bridge{Name: "docker0", Addrs: []net.IPNet{testNet(t, "172.17.0.1/16")}}
	vm := map[string][]types.Node{
		"docker0": {{
			Type:            types.VethType,
			Veth:            "veth0",
			PeerNameInNetns: "eth0",
			NetNsName:       "ns",
			Status:          "up",
			PeerAddrs:       []net.IPNet{testNet(t, "172.17.0.2/16")},
			Master:          master,
		}},
	}
	los := []types.Node{{
		Type:      types.LoType,
		NetNsName: "ns",
		Status:    "up",
		Addrs:     []net.IPNet{testNet(t, "127.0.0.1/8")},
	}}

	var out bytes.Buffer
	Print(&out, vm, nil, nil, los, nil, false)
	for _, want := range []string{"docker0", "172.17.0.1/16", "172.17.0.2/16", "127.0.0.1/8"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("Print() does not contain %q", want)
		}
	}
}
