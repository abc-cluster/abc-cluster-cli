package data

import (
	"fmt"
	"io"
	"os"

	bubblesprogress "github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type progressAdvanceMsg int64

type progressDoneMsg struct{}

type progressModel struct {
	label   string
	total   int64
	current int64
	bar     bubblesprogress.Model
}

func newProgressModel(label string, total int64) progressModel {
	bar := bubblesprogress.New(
		bubblesprogress.WithScaledGradient("#3AA675", "#9BD8A7"),
	)
	bar.Width = 40
	return progressModel{label: label, total: total, bar: bar}
}

func (m progressModel) Init() tea.Cmd {
	return nil
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		if v.Width > 20 {
			m.bar.Width = v.Width - 20
		}
		return m, nil
	case progressAdvanceMsg:
		m.current += int64(v)
		if m.current > m.total {
			m.current = m.total
		}
		return m, nil
	case progressDoneMsg:
		m.current = m.total
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m progressModel) View() string {
	if m.total <= 0 {
		return fmt.Sprintf("%s\n", m.label)
	}
	fraction := float64(m.current) / float64(m.total)
	if fraction > 1 {
		fraction = 1
	}
	return fmt.Sprintf("%s\n%s %6.2f%%\n", m.label, m.bar.ViewAs(fraction), fraction*100)
}

type progressReporter struct {
	program *tea.Program
	done    chan error
	enabled bool
}

func newProgressReporter(out io.Writer, enabled bool, label string, total int64) *progressReporter {
	if !enabled || total <= 0 || !supportsInteractiveProgress(out) {
		return &progressReporter{enabled: false}
	}
	program := tea.NewProgram(
		newProgressModel(label, total),
		tea.WithOutput(out),
		tea.WithInput(nil),
	)
	reporter := &progressReporter{
		program: program,
		done:    make(chan error, 1),
		enabled: true,
	}
	go func() {
		_, err := program.Run()
		reporter.done <- err
	}()
	return reporter
}

func (p *progressReporter) Add(n int64) {
	if !p.enabled || n <= 0 {
		return
	}
	p.program.Send(progressAdvanceMsg(n))
}

func (p *progressReporter) Complete() error {
	if !p.enabled {
		return nil
	}
	p.program.Send(progressDoneMsg{})
	return <-p.done
}

func supportsInteractiveProgress(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
