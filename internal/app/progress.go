package app

import (
	"fmt"
	"io"
	"strings"
)

// progressBar renders a tqdm-style progress bar to the given writer.
type progressBar struct {
	w     io.Writer
	total int
	cur   int
	desc  string
	width int
}

func newProgressBar(w io.Writer, total int, desc string) *progressBar {
	pb := &progressBar{w: w, total: total, desc: desc, width: 30}
	pb.render()
	return pb
}

func (pb *progressBar) SetDescription(desc string) {
	pb.desc = desc
	pb.render()
}

func (pb *progressBar) Increment() {
	pb.cur++
	pb.render()
}

func (pb *progressBar) Finish() {
	pb.render()
	fmt.Fprintln(pb.w)
}

func (pb *progressBar) render() {
	pct := 0.0
	if pb.total > 0 {
		pct = float64(pb.cur) / float64(pb.total)
	}
	filled := int(pct * float64(pb.width))
	if filled > pb.width {
		filled = pb.width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", pb.width-filled)
	fmt.Fprintf(pb.w, "\r%-20s |%s| %d/%d [%3.0f%%]", pb.desc, bar, pb.cur, pb.total, pct*100)
}
