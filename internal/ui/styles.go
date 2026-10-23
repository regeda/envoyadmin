package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/regeda/envoyadmin/internal/snapshot"
)

var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	badStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	// lineNumStyle is a notch darker than dimStyle so the config gutter recedes behind the YAML.
	lineNumStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	logoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	// wordmarkStyle colors the header logo; logoStyle stays cyan for the detail-pane marker.
	wordmarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Bold(true)
)

func markerStyle(h snapshot.Health) lipgloss.Style {
	switch h {
	case snapshot.Unhealthy:
		return badStyle
	case snapshot.Degraded:
		return warnStyle
	case snapshot.Empty:
		return dimStyle
	default:
		return okStyle
	}
}
