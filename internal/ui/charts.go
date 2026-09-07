package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/NimbleMarkets/ntcharts/canvas"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

type taskSlice struct {
	label string
	count int
	color lipgloss.Color
}

func (c taskCounts) slices() []taskSlice {
	return []taskSlice{
		{"Finished", c.finished, green},
		{"In progress", c.active, yellow},
		{"Ready", c.ready, cyan},
		{"Blocked", c.blocked, red},
		{"Other open", max(c.total-c.finished-c.active-c.ready-c.blocked, 0), grey},
	}
}

func gauge(width int, fraction float64, color lipgloss.Color) string {
	p := progress.New(progress.WithWidth(max(width, 1)), progress.WithSolidFill(string(color)), progress.WithoutPercentage(), progress.WithFillCharacters('█', '░'), progress.WithColorProfile(lipgloss.ColorProfile()))
	p.EmptyColor = string(dim)
	return p.ViewAs(min(max(fraction, 0), 1))
}

func fraction(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}

// Each terminal cell holds two square pixels, keeping the pie circular.
func taskPie(c taskCounts, diameter int) string {
	diameter = max(diameter/2*2, 2)
	chart := canvas.New(diameter, diameter/2)
	slices := c.slices()
	colorAt := func(x, y int) lipgloss.Color {
		dx, dy := float64(x)+0.5-float64(diameter)/2, float64(y)+0.5-float64(diameter)/2
		if math.Hypot(dx, dy) > float64(diameter)/2 {
			return ""
		}
		if c.total == 0 {
			return dim
		}
		angle := math.Mod(math.Atan2(dy, dx)+math.Pi/2+2*math.Pi, 2*math.Pi) / (2 * math.Pi)
		sum := 0
		for _, s := range slices {
			sum += s.count
			if angle < fraction(sum, c.total) {
				return s.color
			}
		}
		return grey
	}
	for y := 0; y < diameter/2; y++ {
		for x := 0; x < diameter; x++ {
			top, bottom := colorAt(x, y*2), colorAt(x, y*2+1)
			style := lipgloss.NewStyle()
			mark := ' '
			switch {
			case top != "" && bottom != "":
				mark = '▀'
				style = style.Foreground(top).Background(bottom)
			case top != "":
				mark = '▀'
				style = style.Foreground(top)
			case bottom != "":
				mark = '▄'
				style = style.Foreground(bottom)
			}
			if lipgloss.ColorProfile() == termenv.Ascii {
				color := top
				if color == "" {
					color = bottom
				}
				mark = monochromeSlice(color)
			}
			chart.SetCell(canvas.Point{X: x, Y: y}, canvas.NewCellWithStyle(mark, style))
		}
	}
	return chart.View()
}

func taskOverview(c taskCounts, width int) string {
	var legend strings.Builder
	for _, s := range c.slices() {
		mark := "●"
		if lipgloss.ColorProfile() == termenv.Ascii {
			mark = string(monochromeSlice(s.color))
		}
		fmt.Fprintf(&legend, "%s %-11s %s\n", lipgloss.NewStyle().Foreground(s.color).Render(mark), s.label, countPercent(s.count, c.total))
	}
	legend.WriteString("\nCompletion\n" + gauge(24, fraction(c.finished, c.total), green))
	pie := taskPie(c, 20)
	if width >= 55 {
		return lipgloss.JoinHorizontal(lipgloss.Center, pie, "   ", strings.TrimRight(legend.String(), "\n"))
	}
	return pie + "\n" + legend.String()
}

func priorityCharts(priorities [6]taskCounts, width int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Tasks by priority") + "\n")
	for p, c := range priorities {
		if p == 5 && c.total == 0 {
			continue
		}
		label := fmt.Sprintf("P%d", p)
		if p == 5 {
			label = "Other"
		}
		fmt.Fprintf(&b, "%s: %d tasks  %s %s\n", label, c.total, gauge(min(max(width-32, 6), 28), fraction(c.finished, c.total), green), countPercent(c.finished, c.total))
		fmt.Fprintf(&b, "  Finished %s · Open %s\n", countPercent(c.finished, c.total), countPercent(c.total-c.finished, c.total))
		fmt.Fprintf(&b, "  Active %s · Ready %s · Blocked %s\n", countPercent(c.active, c.total), countPercent(c.ready, c.total), countPercent(c.blocked, c.total))
	}
	return b.String()
}

func monochromeSlice(color lipgloss.Color) rune {
	switch color {
	case green:
		return 'F'
	case yellow:
		return 'I'
	case cyan:
		return 'R'
	case red:
		return 'B'
	case grey:
		return 'O'
	case dim:
		return '·'
	default:
		return ' '
	}
}
