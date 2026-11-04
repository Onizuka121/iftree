package formatter

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/t1anz0ng/iftree/pkg/netutil"
	"github.com/t1anz0ng/iftree/pkg/types"
)

var (
	tableNsColors = colorGrid(1, 5)
)

func Table(w io.Writer, m map[string][]types.Node) error {
	tbStr := strings.Builder{}
	t := table.NewWriter()
	t.SetOutputMirror(&tbStr)

	t.SetTitle(tableTitleStyle.Render("🌉  bridges · 📦 containers · 🔌 veth pairs"))
	t.AppendHeader(table.Row{"🌉 bridge", "📦 netns", "🔌 veth", "🏷 ifname", "● status"})

	for _, bridge := range sortedBridges(m) {
		for _, vp := range m[bridge] {
			if vp.NetNsName == "" {
				logrus.Warnf("no netns name for %+v", vp)
				continue
			}
			id, err := netutil.NsidFromPath(vp.NetNsName)
			if err != nil {
				return errors.Wrapf(err, "get nsid from path `%s`", vp.NetNsName)
			}
			c := netnsColor(id)
			t.AppendRow(table.Row{
				bridgeStyle.Render("🌉 " + bridge),
				netNsStyle.Copy().Foreground(c).Render("📦 " + vp.NetNsName),
				vethStyle.Render("🔌 " + vp.Veth),
				peerStyle.Render("⇄ " + vp.PeerNameInNetns),
				statusLabel(vp.Status),
			})
			t.AppendSeparator()
		}
	}
	t.SetAutoIndex(true)
	t.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, AutoMerge: true},
		{Number: 2, AutoMerge: true},
	})
	t.SetStyle(table.StyleRounded)
	t.Render()

	fmt.Fprintln(w, tableStyle.Render(tbStr.String()))
	return nil
}

func TableParis(w io.Writer, vpairs []types.Node) {
	if len(vpairs) == 0 {
		return
	}
	tbStr := strings.Builder{}
	t := table.NewWriter()
	t.SetOutputMirror(&tbStr)

	t.SetTitle(tableTitleStyle.Render("🔗  unbridged veth pairs"))
	t.AppendHeader(table.Row{"🔌 veth", "🧭 route", "📦 peer"})

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

	for _, v := range pairs {
		route := ""
		if len(v.Route) > 0 && !v.Route.IsUnspecified() {
			route = v.Route.String()
		}
		t.AppendRow(table.Row{
			vethStyle.Render("🔌 " + v.Veth),
			ipStyle.Render(route),
			peerStyle.Render("⇄ " + v.Peer),
		})
		t.AppendSeparator()
	}
	t.SetAutoIndex(true)
	t.SetStyle(table.StyleRounded)
	t.Render()
	fmt.Fprintln(w, tableStyle.Render(tbStr.String()))
}

func hashVethpair(a, b string) string {
	res := []rune(a + b)
	sort.Slice(res, func(i, j int) bool {
		return res[i] < res[j]
	})
	return string(res)
}

func netnsColor(id int) lipgloss.Color {
	index := id % len(tableNsColors)
	if index < 0 {
		index += len(tableNsColors)
	}
	return lipgloss.Color(tableNsColors[index][0])
}
