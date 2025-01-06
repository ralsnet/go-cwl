package cwl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)

const (
	LF = "\n"
)

type TTY struct {
	in      *os.File
	out     *os.File
	r       *bufio.Reader
	termios unix.Termios
	ss      chan os.Signal
	opened  bool
	alt     bool
}

func NewTTY() (*TTY, error) {
	in, err := os.Open("/dev/tty")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(in)

	out, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}

	termios, err := unix.IoctlGetTermios(int(in.Fd()), ioctlReadTermios)
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}

	tty := &TTY{
		in:      in,
		out:     out,
		r:       r,
		termios: *termios,
		ss:      make(chan os.Signal, 1),
		alt:     false,
	}

	return tty, nil
}

func (t *TTY) Open() error {
	if t.opened {
		return nil
	}

	termios, err := unix.IoctlGetTermios(int(t.in.Fd()), ioctlReadTermios)
	if err != nil {
		return err
	}

	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.ICRNL | unix.IGNCR | unix.IXON
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Oflag &^= unix.OPOST
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(int(t.in.Fd()), ioctlWriteTermios, termios); err != nil {
		return err
	}

	if err := syscall.SetNonblock(int(t.in.Fd()), true); err != nil {
		t.Close()
		return err
	}

	t.opened = true

	return nil
}

func (t *TTY) Close() error {
	if !t.opened {
		return nil
	}

	errtermios := unix.IoctlSetTermios(int(t.in.Fd()), ioctlWriteTermios, &t.termios)

	errin := t.in.Close()
	errout := t.out.Close()

	signal.Stop(t.ss)
	close(t.ss)

	t.in = nil
	t.out = nil
	t.r = nil

	if errtermios != nil {
		return errtermios
	}
	if errin != nil {
		return errin
	}
	if errout != nil {
		return errout
	}

	return nil
}

func (t *TTY) Rune() (rune, error) {
	r, _, err := t.r.ReadRune()
	return r, err
}

func (t *TTY) Write(p []byte) (n int, err error) {
	return t.out.Write(p)
}

func (t *TTY) WriteString(s string, args ...any) error {
	_, err := t.out.WriteString(fmt.Sprintf(s, args...))
	return err
}

func (t *TTY) WriteLine(line string) error {
	_, err := t.out.WriteString(line)
	if err != nil {
		return err
	}
	_, err = t.out.WriteString(LF)
	return err
}

func (t *TTY) Size() (int, int, int, int, error) {
	ws, err := unix.IoctlGetWinsize(int(t.out.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return -1, -1, -1, -1, err
	}
	return int(ws.Row), int(ws.Col), int(ws.Xpixel), int(ws.Ypixel), nil
}

func (t *TTY) Clear() error {
	if _, err := t.out.WriteString(ScreenClearAll); err != nil {
		return err
	}
	if _, err := t.out.WriteString(CursorHome); err != nil {
		return err
	}
	return nil
}

func (t *TTY) ClearLine() error {
	if _, err := t.out.WriteString(ScreenClearLine); err != nil {
		return err
	}
	return nil
}

func (t *TTY) EnableAlt() error {
	if !t.opened {
		return nil
	}
	if t.alt {
		return nil
	}
	if _, err := t.out.WriteString(ScreenEnableAlt); err != nil {
		return err
	}
	t.alt = true
	return nil
}

func (t *TTY) DisableAlt() error {
	if !t.opened {
		return nil
	}
	if !t.alt {
		return nil
	}
	if _, err := t.out.WriteString(ScreenDisableAlt); err != nil {
		return err
	}
	t.alt = false
	return nil
}

func (t *TTY) IsAlt() bool {
	return t.alt
}

func (t *TTY) SetCursorPosition(row, col int) error {
	_, err := t.out.WriteString(fmt.Sprintf(CursorMove, row, col))
	return err
}

func (t *TTY) CursorPosition() (int, int, error) {
	deadline := time.After(3 * time.Second)
	ch := make(chan []byte, 1)
	go func() {
		buf := bytes.NewBuffer(nil)
		for {
			r, err := t.Rune()
			if err != nil {
				continue
			}
			buf.WriteRune(r)
			if r == 'R' {
				ch <- buf.Bytes()
				break
			}
		}
	}()

	t.Write([]byte(CursorPosition))

	select {
	case b := <-ch:
		re := regexp.MustCompile(`(\d+);(\d+)`)
		matches := re.FindStringSubmatch(string(b))
		if len(matches) != 3 {
			return 0, 0, errors.New("invalid cursor position")
		}
		row, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, 0, errors.New("invalid cursor position")
		}
		col, err := strconv.Atoi(matches[2])
		if err != nil {
			return 0, 0, errors.New("invalid cursor position")
		}
		return row, col, nil
	case <-deadline:
		return 0, 0, errors.New("timeout")
	}
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
	return t.in
}

func (t *TTY) Output() *os.File {
	return t.out
}

func (t *TTY) HideCursor() error {
	_, err := t.out.WriteString(CursorHide)
	return err
}

func (t *TTY) ShowCursor() error {
	_, err := t.out.WriteString(CursorShow)
	return err
}

func (t *TTY) MoveCursor(row, col int) error {
	_, err := t.out.WriteString(fmt.Sprintf(CursorMove, row, col))
	return err
}
