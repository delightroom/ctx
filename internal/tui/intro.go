package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const introFrameDelay = 90 * time.Millisecond

var introFrames = []struct {
	dog    string
	cat    string
	signal string
}{
	{dog: "woof?", cat: "...", signal: "o---------------"},
	{dog: "woof?", cat: "...", signal: "---o------------"},
	{dog: "woof!", cat: "...", signal: "------o---------"},
	{dog: "woof!", cat: "...", signal: "----------o-----"},
	{dog: "woof!", cat: "meow!", signal: "---------------o"},
	{dog: "...", cat: "meow!", signal: "-----------o----"},
	{dog: "...", cat: "meow!", signal: "-------o--------"},
	{dog: "woof!", cat: "meow!", signal: "---o------------"},
	{dog: "connected", cat: "connected", signal: "-------o--------"},
}

var introFrameCount = len(introFrames)

func (m *Model) renderIntro() string {
	frame := introFrames[m.introFrame%introFrameCount]
	drawing := strings.Join([]string{
		fmt.Sprintf("       %-9s                    %9s", frame.dog, frame.cat),
		"   / \\__                            /\\_/\\",
		"  (    @\\___                       ( o.o )",
		fmt.Sprintf("  /         O=[_]%s[_]=< > ^ <", frame.signal),
		" /   (_____/                       /   \\",
		"/_____/   U                       (_____)",
	}, "\n")
	tagline := lipgloss.JoinHorizontal(
		lipgloss.Center,
		m.styles.title.Render("ctx"),
		m.styles.muted.Render("  context travels better together"),
	)
	hint := m.styles.muted.Render("press any key to skip")
	content := lipgloss.JoinVertical(
		lipgloss.Center,
		drawing,
		"",
		tagline,
		hint,
	)
	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		content,
	)
}
