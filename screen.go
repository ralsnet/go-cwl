package cwl

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

const (
	ScreenClearBelow = "\x1b[0J"
	ScreenClearAbove = "\x1b[1J"
	ScreenClearAll   = "\x1b[2J"
	ScreenClearLine  = "\x1b[2K"
	ScreenScroll     = "\x1b[%dT"
	ScreenReset      = "\x1b[0m"
	ScreenEnableAlt  = "\x1b[?1049h"
	ScreenDisableAlt = "\x1b[?1049l"
	CursorHome       = "\x1b[H"
	CursorUp         = "\x1b[A"
	CursorDown       = "\x1b[B"
	CursorRight      = "\x1b[C"
	CursorLeft       = "\x1b[D"
	CursorMove       = "\x1b[%d;%dH"
	CursorSave       = "\x1b[s"
	CursorRestore    = "\x1b[u"
	CursorPosition   = "\x1b[6n"
	CursorNextLine   = "\x1b[1E"
	CursorPrevLine   = "\x1b[1F"
	CursorHide       = "\x1b[?25l"
	CursorShow       = "\x1b[?25h"
)

type Screen interface {
	Render(ctx context.Context, tty *TTY) error
	HandleInput(ctx context.Context, r rune) (bool, error)
	HandleCtrl(ctx context.Context, ctrl string) (bool, error)
	Init(ctx context.Context)
}

type LoadingScreen struct {
	start time.Time
}

func NewLoadingScreen() *LoadingScreen {
	return &LoadingScreen{
		start: time.Now(),
	}
}

func (s *LoadingScreen) Init(ctx context.Context) {
}

func (s *LoadingScreen) Render(ctx context.Context, tty *TTY) error {
	if err := tty.Clear(); err != nil {
		return err
	}

	elapsed := time.Since(s.start)
	switch int(elapsed.Seconds()) % 3 {
	case 0:
		tty.Write([]byte("Loading."))
	case 1:
		tty.Write([]byte("Loading.."))
	case 2:
		tty.Write([]byte("Loading..."))
	}
	return nil
}

func (s *LoadingScreen) HandleInput(ctx context.Context, r rune) (bool, error) {
	return true, nil
}

func (s *LoadingScreen) HandleCtrl(ctx context.Context, ctrl string) (bool, error) {
	return true, nil
}

type ChooseLogsScreen struct {
	logs     []*LogGroup
	selected []*LogGroup
	index    int
	offset   int
	limit    int
	filter   string
	filtered []*LogGroup
	mode     int
	callback func([]*LogGroup) error
	changed  bool
}

func NewChooseLogsScreen(logs []*LogGroup, selected []*LogGroup, callback func([]*LogGroup) error) *ChooseLogsScreen {
	return &ChooseLogsScreen{
		logs:     logs,
		selected: selected,
		offset:   0,
		limit:    10,
		filtered: logs,
		callback: callback,
		changed:  true,
	}
}

func (s *ChooseLogsScreen) Init(ctx context.Context) {
}

func (s *ChooseLogsScreen) Render(ctx context.Context, tty *TTY) error {
	if !s.changed {
		return nil
	}
	s.changed = false

	if err := tty.Clear(); err != nil {
		return err
	}

	row, col, _, _, err := tty.Size()
	if err != nil {
		return err
	}
	row -= 3

	s.limit = row

	if len(s.filtered[s.offset:]) < s.limit {
		s.limit = len(s.filtered[s.offset:])
	}

	tty.WriteString("\x1b[1mChoose Logs\x1b[0m")
	tty.NextLine(1)
	if s.mode == 1 {
		tty.WriteString("Search (enter to apply): %s", s.filter)
		tty.NextLine(1)
	} else if s.filter != "" && s.mode == 0 {
		tty.WriteString("Search (r: reset): %s", s.filter)
		tty.NextLine(1)
	} else {
		tty.WriteString("(/: search, space: select/unselect, j/k: navigate, enter: apply)")
		tty.NextLine(1)
	}
	tty.NextLine(1)

	for i := s.offset; i < s.offset+s.limit; i++ {
		log := s.filtered[i]
		x := " "
		for _, selected := range s.selected {
			if selected.ARN() == log.ARN() {
				x = "x"
				break
			}
		}
		option := fmt.Sprintf("%3d. [%s] %s (%s)", i+1, x, log.Name(), log.AccountID())
		if len(option) > col-3 {
			option = option[:col-6] + "..."
		}

		if s.index == i {
			tty.WriteString("  \x1b[7m%s\x1b[0m", option)
		} else {
			tty.WriteString("  %s", option)
		}
		tty.NextLine(1)
	}

	return nil
}

func (s *ChooseLogsScreen) HandleInput(ctx context.Context, r rune) (bool, error) {
	s.changed = true
	if s.mode == 1 {
		switch r {
		case 127: // Backspace
			if len(s.filter) > 0 {
				s.filter = s.filter[:len(s.filter)-1]
			}
		case 13: // Enter
			s.mode = 0
			s.filterLogs()
		default:
			if unicode.IsPrint(r) {
				s.filter += string(r)
			}
		}
		return true, nil
	}

	switch r {
	case 'j':
		s.down(ctx)
	case 'k':
		s.up(ctx)
	case ' ':
		contains := false
		for _, selected := range s.selected {
			if selected.ARN() == s.filtered[s.index].ARN() {
				contains = true
				break
			}
		}
		if contains {
			s.selected = slices.DeleteFunc(s.selected, func(i *LogGroup) bool {
				return i.ARN() == s.filtered[s.index].ARN()
			})
		} else {
			s.selected = append(s.selected, s.filtered[s.index])
		}
	case '/': // Filter Input Mode
		s.mode = 1
		s.filter = ""
	case 13: // Enter
		if len(s.selected) == 0 {
			return true, nil
		}
		if err := s.callback(s.selected); err != nil {
			return false, err
		}
	case 'r': // Reset Filter
		s.filter = ""
		s.filterLogs()
	}
	return true, nil
}

func (s *ChooseLogsScreen) HandleCtrl(ctx context.Context, ctrl string) (bool, error) {
	s.changed = true
	switch ctrl {
	case "\x1b[A":
		s.up(ctx)
	case "\x1b[B":
		s.down(ctx)
	}
	return true, nil
}

func (s *ChooseLogsScreen) filterLogs() {
	if s.filter == "" {
		s.filtered = s.logs
		s.index = 0
		s.offset = 0
		return
	}
	s.filtered = []*LogGroup{}
	for _, log := range s.logs {
		if strings.Contains(log.ARN(), s.filter) {
			s.filtered = append(s.filtered, log)
		}
	}
	s.index = 0
	s.offset = 0
}

func (s *ChooseLogsScreen) down(_ context.Context) {
	s.index++
	if s.index >= len(s.filtered) {
		s.index = 0
		s.offset = 0
		return
	}
	if s.index >= s.offset+s.limit {
		s.offset++
	}
}

func (s *ChooseLogsScreen) up(_ context.Context) {
	if s.index == 0 {
		s.index = len(s.filtered) - 1
		s.offset = len(s.filtered) - s.limit
		return
	}
	s.index--
	if s.index < s.offset {
		s.offset--
	}
}

const (
	MaxEvents = 1000
)

type DisplayLogScreen struct {
	log     *LogGroup
	logs    []*LogGroup
	back    func([]*LogGroup)
	buffers map[string][]*LogEvent
	streams map[string]*cloudwatchlogs.StartLiveTailEventStream
	index   map[string]int
	changed map[string]bool
	rw      sync.RWMutex
}

func NewDisplayLogScreen(logs []*LogGroup, back func([]*LogGroup)) *DisplayLogScreen {
	screen := &DisplayLogScreen{
		log:     logs[0],
		logs:    logs,
		back:    back,
		buffers: make(map[string][]*LogEvent, len(logs)),
		streams: make(map[string]*cloudwatchlogs.StartLiveTailEventStream, len(logs)),
		index:   make(map[string]int, len(logs)),
		changed: make(map[string]bool, len(logs)),
	}

	return screen
}

func (s *DisplayLogScreen) Init(ctx context.Context) {
	for _, log := range s.logs {
		go func(log *LogGroup) {
			s.rw.Lock()
			s.buffers[log.ARN()] = []*LogEvent{}
			stream, err := log.Stream(ctx)
			if err != nil {
				return
			}
			s.streams[log.ARN()] = stream
			s.index[log.ARN()] = -1
			s.changed[log.ARN()] = true
			s.rw.Unlock()
			defer func() {
				stream.Close()
				s.rw.Lock()
				delete(s.streams, log.ARN())
				delete(s.index, log.ARN())
				delete(s.changed, log.ARN())
				s.rw.Unlock()
			}()
			for {
				evt := <-stream.Events()
				u, ok := evt.(*types.StartLiveTailResponseStreamMemberSessionUpdate)
				if !ok {
					continue
				}
				if len(u.Value.SessionResults) == 0 {
					continue
				}

				s.rw.Lock()
				s.changed[log.ARN()] = true
				for _, evt := range u.Value.SessionResults {
					s.buffers[log.ARN()] = append(s.buffers[log.ARN()], NewLogEvent(evt))
				}
				if len(s.buffers[log.ARN()]) > MaxEvents {
					s.buffers[log.ARN()] = s.buffers[log.ARN()][len(s.buffers[log.ARN()])-MaxEvents:]
				}
				s.rw.Unlock()
			}
		}(log)
	}
}

func (s *DisplayLogScreen) Render(ctx context.Context, tty *TTY) error {
	if !s.changed[s.log.ARN()] {
		return nil
	}
	s.changed[s.log.ARN()] = false

	if err := tty.Clear(); err != nil {
		return err
	}

	row, col, _, _, err := tty.Size()
	if err != nil {
		return err
	}

	s.rw.RLock()
	defer s.rw.RUnlock()

	events := s.showableEvents(row-3, col)

	buf := bytes.NewBuffer(nil)

	for _, evt := range events {
		buf.WriteString(fmt.Sprintf("\x1b[32m%s\x1b[0m", evt.Timestamp().Format("2006-01-02 15:04:05")))
		buf.WriteString("\n")
		buf.WriteString(fmt.Sprintf("\x1b[33m%s\x1b[0m", evt.Message()))
		buf.WriteString("\n")
	}

	live := fmt.Sprintf("%d/%d", s.index[s.log.ARN()]+1, len(s.buffers[s.log.ARN()]))
	if s.index[s.log.ARN()] == -1 {
		live = fmt.Sprintf("live/%d", len(s.buffers[s.log.ARN()]))
	}

	title := fmt.Sprintf("%s (%s) (%s)", s.log.Name(), s.log.AccountID(), live)
	if len(title) > col-3 {
		title = title[:col-6] + "..."
	}

	tty.WriteString("\x1b[1m%s\x1b[0m", title)
	tty.NextLine(1)
	tty.WriteString("backspace: back")
	tty.NextLine(1)

	body := strings.ReplaceAll(buf.String(), "\n", CursorNextLine)
	tty.WriteString("%s", body)

	return nil
}

func (s *DisplayLogScreen) showableEvents(row, col int) []LogEvent {
	evts := s.buffers[s.log.ARN()]
	index := s.index[s.log.ARN()]

	last := len(evts) - 1
	if index >= 0 {
		last = index
	}

	events := make([]LogEvent, 0, len(evts))

	lines := 0
	for i := last; i >= 0; i-- {
		evt := evts[i]
		lines += len(evt.Lines(col)) + 1
		if lines < row {
			events = append(events, *evt)
		} else {
			break
		}
	}

	if len(events) == 0 && len(evts) > 0 {
		evt := *evts[last]
		lines := evt.Lines(col)
		if len(lines) > row-1 {
			lines = lines[:row-1]
		}
		lastLine := lines[len(lines)-1]
		if len(lastLine) > col-3 {
			lastLine = lastLine[:col-3] + "..."
			lines[len(lines)-1] = lastLine
		}
		evt.msg = strings.Join(lines, "")
		events = append(events, evt)
	}

	slices.Reverse(events)

	return events
}

func (s *DisplayLogScreen) HandleInput(ctx context.Context, r rune) (bool, error) {
	s.rw.Lock()
	defer s.rw.Unlock()
	s.changed[s.log.ARN()] = true
	switch r {
	case 127: // Backspace
		for _, stream := range s.streams {
			stream.Close()
		}
		s.back(s.logs)
	case 'j':
		s.scrollDown(ctx)
	case 'k':
		s.scrollUp(ctx)
	case 'l':
		s.next(ctx)
	case 'h':
		s.prev(ctx)
	}
	return true, nil
}

func (s *DisplayLogScreen) HandleCtrl(ctx context.Context, ctrl string) (bool, error) {
	s.rw.Lock()
	defer s.rw.Unlock()
	switch ctrl {
	case CursorUp:
		s.scrollUp(ctx)
	case CursorDown:
		s.scrollDown(ctx)
	case CursorRight:
		s.next(ctx)
	case CursorLeft:
		s.prev(ctx)
	}
	return true, nil
}

func (s *DisplayLogScreen) scrollUp(_ context.Context) {
	index := s.index[s.log.ARN()]
	if index == -1 {
		s.index[s.log.ARN()] = len(s.buffers[s.log.ARN()]) - 1
	} else if index > 0 {
		s.index[s.log.ARN()] = index - 1
	}
	s.changed[s.log.ARN()] = true
}

func (s *DisplayLogScreen) scrollDown(_ context.Context) {
	index := s.index[s.log.ARN()]
	if index == -1 {
		return
	} else if index < len(s.buffers[s.log.ARN()])-1 {
		s.index[s.log.ARN()] = index + 1
	}
	if index == len(s.buffers[s.log.ARN()])-1 {
		s.index[s.log.ARN()] = -1
	}
	s.changed[s.log.ARN()] = true
}

func (s *DisplayLogScreen) next(_ context.Context) {
	next := slices.Index(s.logs, s.log) + 1
	if next >= len(s.logs) {
		next = 0
	}
	s.log = s.logs[next]
	s.changed[s.log.ARN()] = true
}

func (s *DisplayLogScreen) prev(_ context.Context) {
	next := slices.Index(s.logs, s.log) - 1
	if next < 0 {
		next = len(s.logs) - 1
	}
	s.log = s.logs[next]
	s.changed[s.log.ARN()] = true
}
