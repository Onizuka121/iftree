package formatter

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

var (
	textColor    = lipgloss.AdaptiveColor{Light: "#1e293b", Dark: "#e2e8f0"}
	mutedColor   = lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#94a3b8"}
	accentColor  = lipgloss.AdaptiveColor{Light: "#0f766e", Dark: "#5eead4"}
	purpleColor  = lipgloss.AdaptiveColor{Light: "#6d28d9", Dark: "#c4b5fd"}
	amberColor   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	successColor = lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}
	dangerColor  = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	borderColor  = lipgloss.AdaptiveColor{Light: "#cbd5e1", Dark: "#334155"}

	bannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#052e2b")).
			Background(lipgloss.Color("#5eead4")).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)

	bridgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor)

	netNsStyle = lipgloss.NewStyle().
			Foreground(purpleColor).
			Italic(true)

	vethStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)

	peerStyle = lipgloss.NewStyle().
			Foreground(amberColor)

	ipStyle = lipgloss.NewStyle().
		Foreground(mutedColor)

	treeStyle = lipgloss.NewStyle().
			Foreground(borderColor)

	upStyle = lipgloss.NewStyle().
		Foreground(successColor)

	downStyle = lipgloss.NewStyle().
			Foreground(dangerColor)

	warnStyle = lipgloss.NewStyle().
			Foreground(amberColor)

	tableStyle = lipgloss.NewStyle()

	tableTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#042f2e")).
			Background(lipgloss.Color("#99f6e4")).
			Padding(0, 1)
)

func colorGrid(xSteps, ySteps int) [][]string {
	x0y0, _ := colorful.Hex("#F25D94")
	x1y0, _ := colorful.Hex("#EDFF82")
	x0y1, _ := colorful.Hex("#643AFF")
	x1y1, _ := colorful.Hex("#14F9D5")

	x0 := make([]colorful.Color, ySteps)
	for i := range x0 {
		x0[i] = x0y0.BlendLuv(x0y1, float64(i)/float64(ySteps))
	}

	x1 := make([]colorful.Color, ySteps)
	for i := range x1 {
		x1[i] = x1y0.BlendLuv(x1y1, float64(i)/float64(ySteps))
	}

	grid := make([][]string, ySteps)
	for x := 0; x < ySteps; x++ {
		y0 := x0[x]
		grid[x] = make([]string, xSteps)
		for y := 0; y < xSteps; y++ {
			grid[x][y] = y0.BlendLuv(x1[x], float64(y)/float64(xSteps)).Hex()
		}
	}

	return grid
}
