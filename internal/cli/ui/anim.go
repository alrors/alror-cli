package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

// Animations run only on an interactive terminal. In CI, pipes, --json,
// NO_COLOR or --no-anim, every helper prints its final state immediately,
// so animation never slows down or garbles machine-read output.
var animated bool

// SetAnimated turns terminal animations on or off for this process.
func SetAnimated(on bool) { animated = on }

// Animated reports whether animations are enabled.
func Animated() bool { return animated }

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// SpinFrame returns the spinner frame for the current time, so callers that
// redraw on their own schedule (e.g. bake progress) stay in sync.
func SpinFrame() string {
	return Green.Render(spinFrames[int(time.Now().UnixMilli()/80)%len(spinFrames)])
}

// HideCursor and ShowCursor bracket long animations; ShowCursor is always safe to call.
func HideCursor() {
	if animated {
		fmt.Print("\x1b[?25l")
	}
}

func ShowCursor() {
	if animated {
		fmt.Print("\x1b[?25h")
	}
}

// Reveal prints a multi-line block one line at a time, like a panel sliding in
// or a receipt being printed.
func Reveal(block string, perLine time.Duration) {
	if !animated {
		fmt.Println(block)
		return
	}
	for _, line := range strings.Split(block, "\n") {
		fmt.Println(line)
		time.Sleep(perLine)
	}
}

// Spinner shows an animated status line while slow work happens.
type Spinner struct {
	msg  string
	stop chan struct{}
	done sync.WaitGroup
}

// Spin starts a spinner with a message. Call Stop with the final line.
func Spin(msg string) *Spinner {
	s := &Spinner{msg: msg, stop: make(chan struct{})}
	if !animated {
		return s
	}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			fmt.Printf("\r%s %s\x1b[K", SpinFrame(), Dim.Render(s.msg))
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
		}
	}()
	return s
}

// Stop ends the spinner and replaces its line with final (may be empty to just clear it).
func (s *Spinner) Stop(final string) {
	if animated {
		close(s.stop)
		s.done.Wait()
		fmt.Print("\r\x1b[K")
	}
	if final != "" {
		fmt.Println(final)
	}
}

// Hold keeps a spinner on screen for at least d (used for very fast steps so
// the eye can register them); it is a no-op without animation.
func Hold(msg string, d time.Duration) {
	if !animated {
		return
	}
	s := Spin(msg)
	time.Sleep(d)
	s.Stop("")
}

// Sweep animates a traffic shift as a bar filling from one weight to another,
// then leaves the final bar on its own line.
func Sweep(indent string, from, to, width int, label string) {
	render := func(w float64) string {
		return fmt.Sprintf("%s%s %s %s", indent, Bar(w/100, width),
			Bold.Render(fmt.Sprintf("%3.0f%%", w)), Fainter.Render(label))
	}
	if !animated {
		fmt.Println(render(float64(to)))
		return
	}
	const frames = 14
	for i := 1; i <= frames; i++ {
		// ease-out so the bar settles into place
		t := float64(i) / frames
		eased := 1 - (1-t)*(1-t)
		fmt.Printf("\r%s\x1b[K", render(float64(from)+(float64(to-from))*eased))
		time.Sleep(22 * time.Millisecond)
	}
	fmt.Println()
}

// CountUp animates a number climbing to its final value before the rest of the line.
func CountUp(prefix string, target int, render func(n int) string) {
	if !animated {
		fmt.Println(prefix + render(target))
		return
	}
	steps := 16
	for i := 1; i <= steps; i++ {
		fmt.Printf("\r%s%s\x1b[K", prefix, render(target*i/steps))
		time.Sleep(25 * time.Millisecond)
	}
	fmt.Println()
}

// Interactive reports whether stdout is a terminal (Windows console or TTY).
func Interactive() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
