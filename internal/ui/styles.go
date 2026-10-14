package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/regeda/txenvoy/internal/snapshot"
)

var (
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	badStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	boldStyle = lipgloss.NewStyle().Bold(true)
	logoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
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
