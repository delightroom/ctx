package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type palette struct {
	primary   color.Color
	secondary color.Color
	surface   color.Color
	border    color.Color
	text      color.Color
	muted     color.Color
	inverse   color.Color
	warning   color.Color
	err       color.Color
}

type styles struct {
	header        lipgloss.Style
	panel         lipgloss.Style
	panelActive   lipgloss.Style
	title         lipgloss.Style
	row           lipgloss.Style
	rowSelected   lipgloss.Style
	muted         lipgloss.Style
	success       lipgloss.Style
	warning       lipgloss.Style
	err           lipgloss.Style
	footer        lipgloss.Style
	modal         lipgloss.Style
	modalSelected lipgloss.Style
	spinner       lipgloss.Style
}

func newStyles(dark bool) styles {
	colors := palette{
		primary:   lipgloss.Color("#0F93FC"),
		secondary: lipgloss.Color("#49E209"),
		surface:   lipgloss.Color("#081C39"),
		border:    lipgloss.Color("#BCBEC0"),
		text:      lipgloss.Color("#FFFFFF"),
		muted:     lipgloss.Color("#BCBEC0"),
		inverse:   lipgloss.Color("#000000"),
		warning:   lipgloss.Color("#FFD93D"),
		err:       lipgloss.Color("#FF6B6B"),
	}
	if !dark {
		colors = palette{
			primary:   lipgloss.Color("#0066CC"),
			secondary: lipgloss.Color("#278A00"),
			surface:   lipgloss.Color("#F6F8FA"),
			border:    lipgloss.Color("#8C959F"),
			text:      lipgloss.Color("#1F2328"),
			muted:     lipgloss.Color("#59636E"),
			inverse:   lipgloss.Color("#FFFFFF"),
			warning:   lipgloss.Color("#9A6700"),
			err:       lipgloss.Color("#CF222E"),
		}
	}

	basePanel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colors.border).
		Foreground(colors.text).
		Padding(0, 1)

	return styles{
		header: lipgloss.NewStyle().
			Foreground(colors.text).
			Background(colors.surface).
			Bold(true).
			Padding(0, 1),
		panel: basePanel,
		panelActive: basePanel.
			BorderForeground(colors.primary),
		title: lipgloss.NewStyle().
			Foreground(colors.primary).
			Bold(true),
		row: lipgloss.NewStyle().
			Foreground(colors.text),
		rowSelected: lipgloss.NewStyle().
			Foreground(colors.inverse).
			Background(colors.primary).
			Bold(true),
		muted: lipgloss.NewStyle().
			Foreground(colors.muted),
		success: lipgloss.NewStyle().
			Foreground(colors.secondary).
			Bold(true),
		warning: lipgloss.NewStyle().
			Foreground(colors.warning),
		err: lipgloss.NewStyle().
			Foreground(colors.err).
			Bold(true),
		footer: lipgloss.NewStyle().
			Foreground(colors.muted),
		modal: lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(colors.primary).
			Background(colors.surface).
			Foreground(colors.text).
			Padding(1, 2),
		modalSelected: lipgloss.NewStyle().
			Foreground(colors.inverse).
			Background(colors.primary).
			Bold(true),
		spinner: lipgloss.NewStyle().
			Foreground(colors.primary),
	}
}
