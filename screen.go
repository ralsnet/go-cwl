package cwl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type Screen interface {
	Render(ctx context.Context, tty *TTY) error
	HandleInput(ctx context.Context, r rune) (bool, error)
	HandleCtrl(ctx context.Context, ctrl string) (bool, error)
	HandleMouse(ctx context.Context, code, x, y int) (bool, error)
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

func (s *LoadingScreen) HandleMouse(ctx context.Context, code, x, y int) (bool, error) {
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
		tty.WriteString("(/: search, space: select/unselect, j/k: up/down, h/l: prev/next, enter: apply)")
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
		option := fmt.Sprintf("%3d. [%s] %s (%s:%s)", i+1, x, log.Name(), log.AccountID(), log.Profile())
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
	case 'l':
		s.next(ctx)
	case 'h':
		s.prev(ctx)
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

func (s *ChooseLogsScreen) HandleMouse(ctx context.Context, code, x, y int) (bool, error) {
	switch code {
	case 0:
		if y < 3 {
			return true, nil
		}
		s.changed = true
		clickidx := s.offset + y - 4
		eq := clickidx == s.index
		s.index = s.offset + y - 4
		if s.index < 0 {
			s.index = 0
		}
		if eq {
			return s.HandleInput(ctx, ' ')
		}
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

func (s *ChooseLogsScreen) next(_ context.Context) {
	nextOffset := s.offset + s.limit + 1
	if len(s.filtered)-1 <= nextOffset {
		return
	}
	s.offset = nextOffset
	s.index = nextOffset
}

func (s *ChooseLogsScreen) prev(_ context.Context) {
	prevOffset := s.offset - s.limit
	if prevOffset < 0 {
		prevOffset = 0
	}
	s.offset = prevOffset
	s.index = prevOffset
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
	offset  map[string]int
	live    map[string]bool
	changed map[string]bool
	view    map[string]int
	row     int
	col     int
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
		offset:  make(map[string]int, len(logs)),
		live:    make(map[string]bool, len(logs)),
		changed: make(map[string]bool, len(logs)),
		view:    make(map[string]int, len(logs)),
	}

	return screen
}

func (s *DisplayLogScreen) Init(ctx context.Context) {
	for _, log := range s.logs {
		go func(ctx context.Context, log *LogGroup) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			s.rw.Lock()
			s.buffers[log.ARN()] = []*LogEvent{}
			stream, err := log.Stream(ctx)
			if err != nil {
				return
			}
			s.streams[log.ARN()] = stream
			s.index[log.ARN()] = -1
			s.offset[log.ARN()] = 0
			s.live[log.ARN()] = true
			s.changed[log.ARN()] = true
			s.rw.Unlock()
			defer func(stream *cloudwatchlogs.StartLiveTailEventStream) {
				if err := recover(); err != nil {
					cancel()
				}
				stream.Close()
			}(stream)
			for {
				var evt interface{}
				select {
				case <-ctx.Done():
					return
				case evt = <-stream.Events():
				}
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
		}(ctx, log)
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

	s.handleViewMode(ctx, tty)

	live := s.live[s.log.ARN()]

	buf := bytes.NewBuffer(nil)

	buf.WriteString(fmt.Sprintf("\x1b[32m%s\x1b[0m", s.log.ARN()))
	buf.WriteString(" ")
	status := "paused"
	if live {
		status = "live"
	}
	buf.WriteString(fmt.Sprintf("\x1b[32m%s\x1b[0m", status))
	buf.WriteString("\n")

	if len(s.buffers[s.log.ARN()]) == 0 {
		body := strings.ReplaceAll(buf.String(), "\n", CursorNextLine)
		tty.WriteString("%s", body)
		return nil
	}

	view := s.view[s.log.ARN()]
	if view == viewModeAlt {

		idx := s.index[s.log.ARN()]
		log := s.buffers[s.log.ARN()][idx]

		message := log.Message()

		var b []byte
		// try json.Unmarshal
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(message), &jsonData); err == nil {
			b, _ = json.MarshalIndent(jsonData, "", "  ")
		} else {
			b = []byte(message)
		}

		body := strings.ReplaceAll(string(b), "\n", CursorNextLine)
		tty.WriteString("%s", body)

		return nil
	}

	s.row = row
	s.col = col

	rows := row - 2

	allEvents := s.buffers[s.log.ARN()]
	lastidx := len(s.buffers[s.log.ARN()]) - 1

	if live {
		offset := lastidx - rows
		if offset < 0 {
			offset = 0
		}
		s.offset[s.log.ARN()] = offset
		s.index[s.log.ARN()] = lastidx
	}

	idx := s.index[s.log.ARN()]
	offset := s.offset[s.log.ARN()]
	limit := offset + rows + 1
	if limit > lastidx {
		limit = lastidx + 1
	}
	events := allEvents[offset:limit]

	for i, evt := range events {
		evtidx := i + offset
		timestamp := evt.Timestamp().Format("2006-01-02 15:04:05")
		message := evt.Message()
		chars := len(timestamp) + len(message) + 1
		overflow := col - chars
		if overflow < 0 {
			messageLen := len(message) + overflow - 3
			if messageLen < 0 {
				message = ""
			} else {
				message = message[:messageLen] + "..."
			}
		}

		line := ""
		if evtidx == idx {
			line += "\x1b[7m"
		}
		line += fmt.Sprintf("\x1b[32m%s", timestamp)
		line += " "
		line += fmt.Sprintf("\x1b[33m%s", message)
		line += "\x1b[0m"
		buf.WriteString(line)
		buf.WriteString("\n")
	}

	body := strings.ReplaceAll(buf.String(), "\n", CursorNextLine)

	body = regexp.MustCompile(`ERROR`).ReplaceAllString(body, "\x1b[31m$0\x1b[33m")
	body = regexp.MustCompile(`INFO`).ReplaceAllString(body, "\x1b[32m$0\x1b[33m")
	body = regexp.MustCompile(`WARN`).ReplaceAllString(body, "\x1b[35m$0\x1b[33m")
	body = regexp.MustCompile(`DEBUG`).ReplaceAllString(body, "\x1b[34m$0\x1b[33m")

	tty.WriteString("%s", body)

	return nil
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
		s.cursorDown(ctx, 1)
	case 'k':
		s.cursorUp(ctx, 1)
	case 'J':
		s.cursorDown(ctx, s.row-2)
	case 'K':
		s.cursorUp(ctx, s.row-2)
	case 'l':
		s.next(ctx)
	case 'h':
		s.prev(ctx)
	case ',': // Toggle Live Mode
		if len(s.buffers[s.log.ARN()]) == 0 {
			return true, nil
		}
		s.live[s.log.ARN()] = !s.live[s.log.ARN()]
	case ' ':
		s.viewMode(ctx)
	}
	return true, nil
}

func (s *DisplayLogScreen) HandleCtrl(ctx context.Context, ctrl string) (bool, error) {
	s.rw.Lock()
	defer s.rw.Unlock()
	switch ctrl {
	case CursorUp:
		s.cursorUp(ctx, 1)
	case CursorDown:
		s.cursorDown(ctx, 1)
	case CursorRight:
		s.next(ctx)
	case CursorLeft:
		s.prev(ctx)
	}
	return true, nil
}

func (s *DisplayLogScreen) HandleMouse(ctx context.Context, code, x, y int) (bool, error) {
	s.rw.Lock()
	defer s.rw.Unlock()
	switch code {
	case 0x00: // Left Click
		if y < 2 {
			return true, nil
		}
		if len(s.buffers[s.log.ARN()]) == 0 {
			return true, nil
		}
		lastidx := len(s.buffers[s.log.ARN()]) - 1
		clickidx := s.offset[s.log.ARN()] + y - 2
		if clickidx > lastidx {
			clickidx = lastidx
		}
		if clickidx < 0 {
			clickidx = 0
		}
		curidx := s.index[s.log.ARN()]
		s.index[s.log.ARN()] = clickidx
		s.changed[s.log.ARN()] = true
		if clickidx == curidx {
			s.viewMode(ctx)
		}
	case 0x40: // Wheel Up
		s.cursorUp(ctx, 1)
	case 0x41: // Wheel Down
		s.cursorDown(ctx, 1)
	}
	return true, nil
}

func (s *DisplayLogScreen) cursorUp(_ context.Context, move int) {
	lastidx := len(s.buffers[s.log.ARN()]) - 1
	if lastidx < 0 {
		return
	}

	s.live[s.log.ARN()] = false

	index := s.index[s.log.ARN()] - move
	if index < 0 {
		index = 0
	} else if index > lastidx {
		index = lastidx
	}
	s.index[s.log.ARN()] = index

	offset := s.offset[s.log.ARN()]
	if index < offset {
		s.offset[s.log.ARN()] = index
	} else if index > lastidx {
		s.offset[s.log.ARN()] = lastidx
	}

	s.changed[s.log.ARN()] = true
}

func (s *DisplayLogScreen) cursorDown(_ context.Context, move int) {
	lastidx := len(s.buffers[s.log.ARN()]) - 1
	if lastidx < 0 {
		return
	}

	s.live[s.log.ARN()] = false

	index := s.index[s.log.ARN()] + move
	if index > lastidx {
		index = lastidx
	} else if index < 0 {
		index = 0
	}
	s.index[s.log.ARN()] = index

	offset := s.offset[s.log.ARN()]

	botidx := s.row + offset - 2
	if botidx > lastidx {
		botidx = lastidx
	}

	if index > botidx {
		offset += index - botidx
	}
	if offset > lastidx {
		offset = lastidx
	} else if offset < 0 {
		offset = 0
	}
	s.offset[s.log.ARN()] = offset

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

const (
	viewModeStream   = 0
	viewModeOpenAlt  = 1
	viewModeAlt      = 2
	viewModeCloseAlt = 3
)

func (s *DisplayLogScreen) viewMode(_ context.Context) {
	if len(s.buffers[s.log.ARN()]) == 0 {
		return
	}

	s.live[s.log.ARN()] = false
	switch s.view[s.log.ARN()] {
	case viewModeStream:
		s.view[s.log.ARN()] = viewModeOpenAlt
	case viewModeAlt:
		s.view[s.log.ARN()] = viewModeCloseAlt
	}
	s.changed[s.log.ARN()] = true
}

func (s *DisplayLogScreen) handleViewMode(_ context.Context, tty *TTY) {
	if len(s.buffers[s.log.ARN()]) == 0 {
		return
	}

	view := s.view[s.log.ARN()]
	switch view {
	case viewModeOpenAlt:
		tty.Clear()
		s.view[s.log.ARN()] = viewModeAlt
		tty.DisableMouse()

	case viewModeCloseAlt:
		tty.Clear()
		s.view[s.log.ARN()] = viewModeStream
		tty.EnableMouse()

	default:
		return
	}
}
