package cwl

import (
	"fmt"
	"os"

	"github.com/mattn/go-tty"
)

const (
	LF = "\n"
)

type TTY struct {
	t   *tty.TTY
	alt bool
}

func NewTTY() (*TTY, error) {
	return &TTY{}, nil
}

func (t *TTY) Open() error {
	var err error
	t.t, err = tty.Open()
	if err != nil {
		return err
	}
	t.EnableAlt()
	t.HideCursor()
	return nil
}

func (t *TTY) Close() error {
	if t.t != nil {
		t.DisableAlt()
		t.ShowCursor()
		t.t.Close()
	}
	return nil
}

func (t *TTY) Rune() (rune, error) {
	return t.t.ReadRune()
}

func (t *TTY) Write(p []byte) (n int, err error) {
	return t.t.Output().Write(p)
}

func (t *TTY) WriteString(s string, args ...any) error {
	_, err := t.t.Output().WriteString(fmt.Sprintf(s, args...))
	return err
}

func (t *TTY) WriteLine(line string) error {
	_, err := t.t.Output().WriteString(line)
	if err != nil {
		return err
	}
	_, err = t.t.Output().WriteString(LF)
	return err
}

func (t *TTY) Size() (int, int, int, int, error) {
	col, row, xpixel, ypixel, err := t.t.SizePixel()
	if err != nil {
		return -1, -1, -1, -1, err
	}
	return row, col, xpixel, ypixel, nil
}

func (t *TTY) Clear() error {
	if _, err := t.t.Output().WriteString(ScreenClearAll); err != nil {
		return err
	}
	if _, err := t.t.Output().WriteString(CursorHome); err != nil {
		return err
	}
	return nil
}

func (t *TTY) ClearLine() error {
	if _, err := t.t.Output().WriteString(ScreenClearLine); err != nil {
		return err
	}
	return nil
}

func (t *TTY) EnableAlt() error {
	if t.t == nil {
		return nil
	}
	if t.alt {
		return nil
	}
	if _, err := t.t.Output().WriteString(ScreenEnableAlt); err != nil {
		return err
	}
	t.alt = true
	return nil
}

func (t *TTY) DisableAlt() error {
	if t.t == nil {
		return nil
	}
	if !t.alt {
		return nil
	}
	if _, err := t.t.Output().WriteString(ScreenDisableAlt); err != nil {
		return err
	}
	t.alt = false
	return nil
}

func (t *TTY) IsAlt() bool {
	return t.alt
}

func (t *TTY) NextLine(n int) error {
	if n == 0 {
		t.Write([]byte(CursorNextLine))
		return nil
	}
	for i := 0; i < n; i++ {
		t.Write([]byte(CursorNextLine))
	}
	return nil
}

func (t *TTY) PrevLine(n int) error {
	if n == 0 {
		t.Write([]byte(CursorPrevLine))
		return nil
	}
	for i := 0; i < n; i++ {
		t.Write([]byte(CursorPrevLine))
	}
	return nil
}

func (t *TTY) Input() *os.File {
	return t.t.Input()
}

func (t *TTY) Output() *os.File {
	return t.t.Output()
}

func (t *TTY) HideCursor() error {
	_, err := t.t.Output().WriteString(CursorHide)
	return err
}

func (t *TTY) ShowCursor() error {
	_, err := t.t.Output().WriteString(CursorShow)
	return err
}

func (t *TTY) MoveCursor(row, col int) error {
	_, err := t.t.Output().WriteString(fmt.Sprintf(CursorMove, row, col))
	return err
}
